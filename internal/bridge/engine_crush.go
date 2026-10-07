package bridge

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"agentgo/internal/engine"
	"agentgo/internal/engine/crushengine"
	"agentgo/internal/plugin"
	"agentgo/internal/workflow"
)

// Crush coding engine (opt-in sidecar). Environment:
//
//	AGENTGO_CRUSH_EXE          path to crush(.exe); default: next to the app, or ..\crush\
//	AGENTGO_CRUSH_CONFIG_DIR   dir holding crush.json (providers/models/mcp);
//	                           default: inherited CRUSH_GLOBAL_CONFIG
//	AGENTGO_CRUSH_AUTOAPPROVE  "0" = deny every tool permission (read-only until the
//	                           approval UI lands); default "1" (workspace-scoped yolo)
const crushEnvExe = "AGENTGO_CRUSH_EXE"

// findCrushExe locates the Crush binary, or returns "" when the engine should stay disabled.
func findCrushExe() string {
	if p := strings.TrimSpace(os.Getenv(crushEnvExe)); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		log.Printf("[crush] %s=%q does not exist; crush engine disabled", crushEnvExe, p)
		return ""
	}
	name := "crush"
	if os.PathSeparator == '\\' {
		name = "crush.exe"
	}
	var cands []string
	if self, err := os.Executable(); err == nil {
		dir := filepath.Dir(self)
		cands = append(cands, filepath.Join(dir, name), filepath.Join(dir, "..", "crush", name))
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// crushPlugin contributes the Crush sidecar as an engine and the coding_agent
// workflow node. It is omitted (ok=false) when no crush binary is available.
func crushPlugin(rt *Runtime) (plugin.Plugin, bool) {
	exe := findCrushExe()
	if exe == "" || rt == nil {
		return plugin.Plugin{}, false
	}
	return plugin.Plugin{
		Name:     "crush",
		Provides: []string{svcCrush},
		Inject:   []string{svcEngines},
		Apply: func(c *plugin.Context) error {
			router, err := plugin.Use[*engine.Router](c, svcEngines)
			if err != nil {
				return err
			}
			cfg := crushengine.Config{
				Exe:              exe,
				ConfigDir:        strings.TrimSpace(os.Getenv("AGENTGO_CRUSH_CONFIG_DIR")),
				DefaultWorkspace: rt.WorkspaceRoot(),
				AutoApprove:      os.Getenv("AGENTGO_CRUSH_AUTOAPPROVE") != "0",
			}
			if dd := rt.DataDir(); dd != "" {
				cfg.LogPath = filepath.Join(dd, "logs", "crush-server.log")
				_ = os.MkdirAll(filepath.Dir(cfg.LogPath), 0o755)
			}
			ce := crushengine.New(cfg)
			c.Effect("stop crush sidecar", ce.Close) // registered first => released last
			if err := c.Provide(svcCrush, ce); err != nil {
				return err
			}
			c.Contribute("engine", crushengine.EngineID, router.Register(ce))

			node := &workflow.CodingAgentNodeExecutor{Run: rt.codingAgentRun}
			for _, typ := range []string{"coding_agent", "crush"} {
				dispose, err := workflow.RegisterNodeExecutor(typ, node)
				if err != nil {
					return err
				}
				c.Contribute("workflow_node", typ, dispose)
			}
			log.Printf("[crush] exe=%s autoapprove=%v", exe, cfg.AutoApprove)
			return nil
		},
	}, true
}

// crushEngine is the Crush sidecar engine while the crush plugin is running, else nil.
func (s *AppService) crushEngine() *crushengine.Engine {
	if s == nil || s.plugins == nil {
		return nil
	}
	v, ok := s.plugins.Service(svcCrush)
	if !ok {
		return nil
	}
	ce, _ := v.(*crushengine.Engine)
	return ce
}

// ServiceShutdown stops plugins (and with them the Crush sidecar) so nothing is orphaned.
func (s *AppService) ServiceShutdown() error {
	s.Close()
	return nil
}

// Close stops every plugin, dependents first (idempotent). Headless hosts should call it on exit.
func (s *AppService) Close() {
	if s.unbindWorkspace != nil {
		s.unbindWorkspace() // a closed service must not be reloaded by a later switch
		s.unbindWorkspace = nil
	}
	// The host is shared with the runtime, so this also stops the runtime's own
	// plugins, dependents first.
	if s.plugins != nil {
		if err := s.plugins.Shutdown(); err != nil {
			log.Printf("[plugin] shutdown: %v", err)
		}
	}
}

// codingChangeSink folds the event stream into the workflow node result.
type codingChangeSink struct {
	mu      sync.Mutex
	order   []string
	changes map[string]*workflow.CodingChange
	calls   int
}

func (c *codingChangeSink) Emit(e engine.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e.Type {
	case engine.EventToolCall:
		c.calls++
	case engine.EventFileChange:
		p := e.Payload
		path, _ := p["path"].(string)
		if path == "" {
			return
		}
		before, _ := p["before"].(string)
		after, _ := p["after"].(string)
		if ch, ok := c.changes[path]; ok {
			ch.After = after // keep the first "before", track the latest "after"
			return
		}
		if c.changes == nil {
			c.changes = map[string]*workflow.CodingChange{}
		}
		c.changes[path] = &workflow.CodingChange{Path: path, Before: before, After: after}
		c.order = append(c.order, path)
	}
}

// codingAgentRun backs the CodingAgent workflow node.
func (r *Runtime) codingAgentRun(ctx context.Context, req workflow.CodingRequest) (workflow.CodingResult, error) {
	router := r.engineRouter
	if router == nil {
		return workflow.CodingResult{}, fmt.Errorf("coding engine unavailable: set %s to the crush executable", crushEnvExe)
	}
	id := req.Engine
	if id == "" {
		id = crushengine.EngineID
	}
	dir := req.WorkDir
	if dir == "" {
		dir = r.WorkspaceRoot()
	}
	sink := &codingChangeSink{}
	res := router.Run(ctx, engine.RunRequest{
		Engine: id, SessionID: req.SessionID, Input: req.Prompt, Workspace: dir,
	}, sink)
	out := workflow.CodingResult{Engine: id, ToolCalls: sink.calls}
	for _, p := range sink.order {
		out.ChangedFiles = append(out.ChangedFiles, *sink.changes[p])
	}
	if len(res.Messages) > 0 {
		out.Summary = res.Messages[len(res.Messages)-1].Content
	}
	if res.Error != "" {
		return out, fmt.Errorf("coding agent (%s): %s", id, res.Error)
	}
	if res.Pending {
		return out, fmt.Errorf("coding agent (%s): waiting for approval, which workflows cannot grant yet", id)
	}
	return out, nil
}
