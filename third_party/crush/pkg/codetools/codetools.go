// Package codetools exposes Crush's code-aware tools (LSP diagnostics,
// references, symbols, definition; view/edit/multiedit/write; grep/glob/ls)
// behind a small, stable surface that leaks neither Crush internals nor
// fantasy types. It is maintained in the AgentGo fork so that other agent
// loops can reuse the tool implementations in-process.
package codetools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"

	"agentgo/third_party/crush/internal/agent/tools"
	"agentgo/third_party/crush/internal/config"
	"agentgo/third_party/crush/internal/lsp"
	"agentgo/third_party/crush/internal/permission"
	"agentgo/third_party/crush/internal/pubsub"
	"agentgo/third_party/crush/internal/skills"
)

// Request describes a tool action that needs approval.
type Request struct {
	SessionID   string
	ToolCallID  string
	ToolName    string
	Action      string
	Description string
	Path        string
	Params      any
}

// Approver decides one permission request; return true to allow it.
type Approver func(ctx context.Context, r Request) bool

// Options configures a Toolbox.
type Options struct {
	// WorkDir is the project root the tools operate in (required).
	WorkDir string
	// DataDir holds Crush's per-workspace state (LSP cache, spill files).
	// Defaults to <WorkDir>/.crush.
	DataDir string
	// Approver is asked before mutating or sensitive actions. Nil means
	// everything is allowed without asking.
	Approver Approver
	// LSPOnly builds just the language-server tools (diagnostics, references,
	// symbols, definition, call hierarchy). AgentGo sets it once its native
	// file tools (internal/coderuntime, internal/codetools) replace Crush's
	// view/edit/multiedit/write/grep/glob/ls.
	LSPOnly bool
}

// Result is a tool's output.
type Result struct {
	Content string
	IsError bool
}

// Tool is a framework-neutral tool definition.
type Tool struct {
	Name        string
	Description string
	// Schema is a complete JSON Schema object ({"type":"object",...}).
	Schema   map[string]any
	Parallel bool
	// Run executes the tool. argsJSON is the model's JSON arguments.
	Run func(ctx context.Context, sessionID, callID, argsJSON string) (Result, error)
}

// LSPClientInfo holds information about an LSP client's state.
type LSPClientInfo struct {
	Name            string    `json:"name"`
	State           int       `json:"state"`
	Error           string    `json:"error,omitempty"`
	DiagnosticCount int       `json:"diagnostic_count"`
	ConnectedAt     time.Time `json:"connected_at"`
}

// LSPEvent represents a change in LSP server state or diagnostics.
type LSPEvent struct {
	Type            string `json:"type"` // "state_changed" | "diagnostics_changed"
	Name            string `json:"name"`
	State           int    `json:"state"`
	Error           string `json:"error,omitempty"`
	DiagnosticCount int    `json:"diagnostic_count"`
}

// Toolbox owns the services (LSP manager, permissions) behind the tools.
type Toolbox struct {
	Tools []Tool

	lsp       *lsp.Manager
	cancel    context.CancelFunc
	closeOnce sync.Once

	mu         sync.RWMutex
	lspStates  map[string]LSPClientInfo
	onLSPEvent func(e LSPEvent)
}

// New builds the toolbox. LSP servers start lazily when a tool touches a file.
func New(opts Options) (*Toolbox, error) {
	if strings.TrimSpace(opts.WorkDir) == "" {
		return nil, errors.New("codetools: WorkDir is required")
	}
	workDir, err := filepath.Abs(opts.WorkDir)
	if err != nil {
		return nil, err
	}
	dataDir := opts.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(workDir, ".crush")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("codetools: data dir: %w", err)
	}
	store, err := config.Load(workDir, dataDir, false)
	if err != nil {
		return nil, fmt.Errorf("codetools: load config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	perms := permission.NewPermissionService(workDir, opts.Approver == nil, nil)
	if opts.Approver != nil {
		go servePermissions(ctx, perms, opts.Approver)
	}
	lspMgr := lsp.NewManager(store)
	files := newMemHistory()
	tracker := newMemTracker()
	cfg := store.Config()

	var fa []fantasy.AgentTool
	if !opts.LSPOnly {
		fa = append(fa,
			tools.NewViewTool(lspMgr, perms, tracker, skills.NewTracker(nil), workDir, cfg.Options.SkillsPaths...),
			tools.NewEditTool(lspMgr, perms, files, tracker, workDir),
			tools.NewMultiEditTool(lspMgr, perms, files, tracker, workDir),
			tools.NewWriteTool(lspMgr, perms, files, tracker, workDir),
			tools.NewGlobTool(workDir, cfg.Tools.Glob),
			tools.NewGrepTool(workDir, cfg.Tools.Grep),
			tools.NewLsTool(perms, workDir, cfg.Tools.Ls),
		)
	}
	fa = append(fa,
		tools.NewDiagnosticsTool(lspMgr),
		tools.NewReferencesTool(lspMgr),
		tools.NewSymbolsTool(lspMgr),
		tools.NewDefinitionTool(lspMgr),
		tools.NewCallHierarchyTool(lspMgr),
	)
	tb := &Toolbox{
		lsp:       lspMgr,
		cancel:    cancel,
		lspStates: make(map[string]LSPClientInfo),
	}
	lspMgr.SetCallback(func(name string, client *lsp.Client) {
		if client == nil {
			tb.mu.Lock()
			if _, exists := tb.lspStates[name]; !exists {
				tb.lspStates[name] = LSPClientInfo{Name: name, State: 0}
			}
			tb.mu.Unlock()
			return
		}
		tb.updateClientState(client, "state_changed")
		client.SetDiagnosticsCallback(func(name string, count int) {
			tb.updateClientState(client, "diagnostics_changed")
		})
	})
	for _, t := range fa {
		tb.Tools = append(tb.Tools, wrap(t))
	}
	go tb.primeLSPs(workDir)
	return tb, nil
}

func (t *Toolbox) primeLSPs(workDir string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	candidates := []string{
		"go.mod", "main.go",
		"Cargo.toml",
		"package.json", "tsconfig.json",
		"pyproject.toml", "requirements.txt",
	}
	for _, f := range candidates {
		path := filepath.Join(workDir, f)
		if _, err := os.Stat(path); err == nil {
			t.lsp.Start(ctx, path)
		}
	}
}

// Close stops background work and any LSP servers that were started.
func (t *Toolbox) Close() {
	t.closeOnce.Do(func() {
		t.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5e9)
		defer cancel()
		t.lsp.StopAll(ctx)
	})
}

func (t *Toolbox) updateClientState(client *lsp.Client, evType string) {
	if client == nil {
		return
	}
	counts := client.GetDiagnosticCounts()
	totalDiags := counts.Error + counts.Warning + counts.Information + counts.Hint
	state := int(client.GetServerState())
	name := client.GetName()

	t.mu.Lock()
	info, exists := t.lspStates[name]
	if !exists || info.ConnectedAt.IsZero() {
		info = LSPClientInfo{
			Name:        name,
			ConnectedAt: time.Now(),
		}
	}
	info.State = state
	info.DiagnosticCount = totalDiags
	t.lspStates[name] = info
	cb := t.onLSPEvent
	t.mu.Unlock()

	if cb != nil {
		cb(LSPEvent{
			Type:            evType,
			Name:            name,
			State:           state,
			DiagnosticCount: totalDiags,
		})
	}
}

// LSPStates returns the current state of all known LSP clients.
func (t *Toolbox) LSPStates() map[string]LSPClientInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lsp != nil && t.lsp.Clients() != nil {
		for client := range t.lsp.Clients().Seq() {
			if client != nil {
				name := client.GetName()
				counts := client.GetDiagnosticCounts()
				totalDiags := counts.Error + counts.Warning + counts.Information + counts.Hint
				state := int(client.GetServerState())
				info, exists := t.lspStates[name]
				if !exists || info.ConnectedAt.IsZero() {
					info = LSPClientInfo{Name: name, ConnectedAt: time.Now()}
				}
				info.State = state
				info.DiagnosticCount = totalDiags
				t.lspStates[name] = info
			}
		}
	}
	out := make(map[string]LSPClientInfo, len(t.lspStates))
	for k, v := range t.lspStates {
		out[k] = v
	}
	return out
}

// LSPDiagnostics returns diagnostics for a specific LSP client.
func (t *Toolbox) LSPDiagnostics(name string) any {
	if t.lsp == nil || t.lsp.Clients() == nil {
		return map[string]any{}
	}
	if client, ok := t.lsp.Clients().Get(name); ok && client != nil {
		return client.GetDiagnostics()
	}
	return map[string]any{}
}

// StartLSP starts an LSP server that can handle the given file path.
func (t *Toolbox) StartLSP(ctx context.Context, path string) {
	if t.lsp != nil {
		t.lsp.Start(ctx, path)
	}
}

// StopAllLSPs stops all active LSP servers.
func (t *Toolbox) StopAllLSPs(ctx context.Context) {
	if t.lsp != nil {
		t.lsp.StopAll(ctx)
	}
}

// SetLSPEventListener registers a listener for LSP state and diagnostic changes.
func (t *Toolbox) SetLSPEventListener(fn func(e LSPEvent)) {
	t.mu.Lock()
	t.onLSPEvent = fn
	t.mu.Unlock()
}

func wrap(t fantasy.AgentTool) Tool {
	info := t.Info()
	schema := map[string]any{"type": "object", "properties": info.Parameters}
	if len(info.Required) > 0 {
		schema["required"] = info.Required
	}
	if info.Parameters == nil {
		schema["properties"] = map[string]any{}
	}
	return Tool{
		Name: info.Name, Description: info.Description, Schema: schema, Parallel: info.Parallel,
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (Result, error) {
			ctx = context.WithValue(ctx, tools.SessionIDContextKey, sessionID)
			if strings.TrimSpace(argsJSON) == "" {
				argsJSON = "{}"
			}
			resp, err := t.Run(ctx, fantasy.ToolCall{ID: callID, Name: info.Name, Input: argsJSON})
			if err != nil {
				return Result{}, err
			}
			return Result{Content: resp.Content, IsError: resp.IsError}, nil
		},
	}
}

// servePermissions answers Crush's blocking permission requests via approver.
func servePermissions(ctx context.Context, svc permission.Service, approve Approver) {
	for ev := range svc.Subscribe(ctx) {
		if ev.Type != pubsub.CreatedEvent {
			continue
		}
		req := ev.Payload
		go func() {
			ok := approve(ctx, Request{
				SessionID: req.SessionID, ToolCallID: req.ToolCallID, ToolName: req.ToolName,
				Action: req.Action, Description: req.Description, Path: req.Path, Params: req.Params,
			})
			if ok {
				svc.Grant(req)
			} else {
				svc.Deny(req)
			}
		}()
	}
}
