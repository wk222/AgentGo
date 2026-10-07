package bridge

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"

	"agentgo/internal/agent"
	"agentgo/internal/applog"
	"agentgo/internal/capability"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// The runtime is assembled by the plugin host. NewRuntime only decides where
// the data lives, reads configuration, registers the runtime plugins
// (runtime_plugins_*.go) and starts them. Each plugin builds one service,
// publishes it under a name, and hands every goroutine, listener and handle it
// starts to its Scope, so that Close can release all of it, dependents first.
//
// Runtime stays the bridge's composition object: plugins also store what they
// build in its fields, which the many existing call sites read. New code gets
// its dependencies from the host (plugin.Use) instead.

// Service names published by the runtime plugins.
const (
	svcModel       = "model"       // ModelSource
	svcDB          = "db"          // *sql.DB, the shared SQLite database
	svcMemory      = "memory"      // memory.Engine
	svcSessions    = "sessions"    // *sessions.Store
	svcApprovals   = "approvals"   // *governance.ApprovalQueue
	svcCheckpoints = "checkpoints" // *checkpoint.SQLiteStore
	svcWorkspace   = "workspace"   // *workspace.ContextMiddleware
	svcWorkflow    = "workflow"    // *workflow.Store
	svcAgent       = "agent"       // *agent.Runner
	svcApps        = "apps"        // *apps.Store
	svcToolset     = "toolset"     // bool: the built-in tools are registered
	svcTaskHub     = "taskhub"     // *taskhub.Hub
	svcIDE         = "ide"         // bool: process supervision, file system, terminals
)

// pluginsDisabledEnv lists plugins to keep from starting, comma separated.
// config.json can do the same with {"plugins": {"disabled": [...]}}.
const pluginsDisabledEnv = "AGENTGO_PLUGINS_DISABLE"

// DefaultDataDir is the user's AgentGo data directory. Surfaces that look for a
// running host (see internal/hostlink) need it before they have a Runtime.
func DefaultDataDir() string {
	p, err := xdg.DataFile("agentgo")
	if err != nil {
		return filepath.Join(".", "data")
	}
	return filepath.Dir(p)
}

// NewRuntime assembles the runtime in the user's data directory.
func NewRuntime() (*Runtime, error) {
	dataDir := DefaultDataDir()
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if err := applog.Init(dataDir); err != nil {
		log.Printf("[agentgo] file log disabled: %v", err)
	}
	return newRuntimeAt(dataDir)
}

// newRuntimeAt assembles a runtime whose database and files live in dataDir.
// It does not touch process-wide logging, so tests can run it in a temp dir.
// ModelSource is how everything that talks to a model learns which one and with
// what credentials. The "model" plugin provides it; a different implementation
// (another provider, a router, a test double) can replace that plugin and the
// consumers do not change, because they ask the host for the service by name.
type ModelSource interface {
	LLM() LLMConfig
}

// configModel is the default ModelSource: the model configured in config.json
// and changed through the settings screen.
type configModel struct{ rt *Runtime }

func (m configModel) LLM() LLMConfig {
	m.rt.mu.RLock()
	defer m.rt.mu.RUnlock()
	return m.rt.llm
}

// llmSettingsFrom adapts a ModelSource to the form the agent code takes. It
// reads the source on every call, so a changed configuration applies at once.
func llmSettingsFrom(src ModelSource) func() agent.LLMSettings {
	return func() agent.LLMSettings {
		c := src.LLM()
		return agent.LLMSettings{APIBase: c.APIBase, APIKey: c.APIKey, Model: c.Model, FallbackModel: c.FallbackModel}
	}
}

func newRuntimeAt(dataDir string) (*Runtime, error) {
	return newRuntimeWith(dataDir, nil)
}

// newRuntimeWith is newRuntimeAt that lets the caller change the plugin list
// first: swap an implementation, add a probe. Production passes nil.
func newRuntimeWith(dataDir string, adjust func([]plugin.Plugin) []plugin.Plugin) (*Runtime, error) {
	cfg, govCfg, wsCfg := loadAppConfig(dataDir)
	log.Printf("[agentgo] data_dir=%s db=%s governance=%s", dataDir, filepath.Join(dataDir, "agentgo.db"), govCfg.ControlMode)

	rt := &Runtime{
		dataDir: dataDir, llm: cfg, governanceCfg: govCfg, workspaceHint: wsCfg.Root,
		interactStore:    tools.NewInteractionStore(),
		runTrack:         NewRunTracker(),
		workspaceReviews: newWorkspaceReviewStore(),
	}
	rt.host = plugin.NewHost(governanceHooksFor(func() *capability.Bus { return rt.capBus }))

	plugins := rt.runtimePlugins()
	if adjust != nil {
		plugins = adjust(plugins)
	}
	for _, p := range plugins {
		if err := rt.host.Register(p); err != nil {
			return nil, fmt.Errorf("runtime: register plugin %s: %w", p.Name, err)
		}
	}
	for _, name := range disabledPlugins(dataDir) {
		if err := rt.host.Disable(name); err != nil {
			log.Printf("[plugin] cannot disable %q: %v", name, err)
		}
	}
	if err := rt.host.Start(); err != nil {
		_ = rt.host.Shutdown()
		return nil, fmt.Errorf("runtime: start plugins: %w", err)
	}
	// Optional plugins may fail or be absent; the runtime cannot work without
	// the protected ones, so say which one failed and why rather than limp on.
	for _, st := range rt.host.Statuses() {
		if st.Protected && st.State != plugin.StateRunning {
			reason := st.Error
			if reason == "" {
				reason = string(st.State)
			}
			_ = rt.host.Shutdown()
			return nil, fmt.Errorf("runtime: required plugin %q is %s: %s", st.Name, st.State, reason)
		}
	}
	return rt, nil
}

// Close releases everything the runtime started: plugins stop dependents
// first, so no consumer outlives the service it uses. Safe to call twice.
func (rt *Runtime) Close() error {
	if rt == nil || rt.host == nil {
		return nil
	}
	return rt.host.Shutdown()
}

// PluginHost is the host that owns the runtime's services. Nil for a Runtime
// that was assembled by hand (tests).
func (rt *Runtime) PluginHost() *plugin.Host { return rt.host }

// disabledPlugins reads the plugins configuration asks not to start.
func disabledPlugins(dataDir string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	for _, n := range strings.Split(os.Getenv(pluginsDisabledEnv), ",") {
		add(n)
	}
	if b, err := os.ReadFile(configPath(dataDir)); err == nil {
		var stored struct {
			Plugins struct {
				Disabled []string `json:"disabled"`
			} `json:"plugins"`
		}
		if json.Unmarshal(b, &stored) == nil {
			for _, n := range stored.Plugins.Disabled {
				add(n)
			}
		}
	}
	return out
}
