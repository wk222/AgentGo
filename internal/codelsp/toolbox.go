package codelsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentgo/internal/coderuntime"
)

// Options configures a Toolbox.
type Options struct {
	WorkDir string // project root (required)
	// Overrides are the user's server settings; see LoadOverrides.
	Overrides Overrides
	// NoAuto stops bundled servers from starting on their own.
	NoAuto bool
	// Search finds symbol positions for lsp_references / definition /
	// call_hierarchy. Without it those tools report the symbol as not found.
	Search SymbolSearch
}

// Toolbox is the native coderuntime.Toolbox for the language-server tools.
// File tools are not part of it: they are layered on top by the caller.
type Toolbox struct {
	m     *Manager
	tools []coderuntime.Tool
	root  string

	cancel    context.CancelFunc
	closeOnce sync.Once

	mu       sync.Mutex
	states   map[string]coderuntime.LSPClientInfo
	listener func(coderuntime.LSPEvent)
}

var _ coderuntime.Toolbox = (*Toolbox)(nil)

// New builds the toolbox. Servers start lazily when a tool touches a file.
func New(o Options) (*Toolbox, error) {
	if strings.TrimSpace(o.WorkDir) == "" {
		return nil, errors.New("codelsp: WorkDir is required")
	}
	root, err := filepath.Abs(o.WorkDir)
	if err != nil {
		return nil, err
	}
	m := NewManager(ManagerOptions{WorkDir: root, Overrides: o.Overrides, NoAuto: o.NoAuto})
	ctx, cancel := context.WithCancel(context.Background())
	t := &Toolbox{m: m, root: root, cancel: cancel, states: map[string]coderuntime.LSPClientInfo{}}
	t.tools = newTools(m, root, o.Search)
	m.SetCallback(func(name string, c *Client) {
		if c == nil {
			return
		}
		t.update(c, "state_changed")
		c.SetDiagnosticsCallback(func(string, int) { t.update(c, "diagnostics_changed") })
	})
	go t.prime(ctx)
	return t, nil
}

// prime starts servers for the project files that name their language, so the
// first lsp_* call does not pay the startup.
func (t *Toolbox) prime(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, f := range []string{"go.mod", "main.go", "Cargo.toml", "package.json", "tsconfig.json", "pyproject.toml", "requirements.txt"} {
		p := filepath.Join(t.root, f)
		if _, err := os.Stat(p); err == nil {
			t.m.Start(ctx, p)
		}
	}
}

// Tools returns the five lsp_* tools.
func (t *Toolbox) Tools() []coderuntime.Tool { return t.tools }

func (t *Toolbox) update(c *Client, evType string) {
	total := c.Counts().Total()
	state := int(c.State())
	t.mu.Lock()
	info, ok := t.states[c.name]
	if !ok || info.ConnectedAt.IsZero() {
		info = coderuntime.LSPClientInfo{Name: c.name, ConnectedAt: time.Now()}
	}
	info.State, info.DiagnosticCount = state, total
	t.states[c.name] = info
	cb := t.listener
	t.mu.Unlock()
	if cb != nil {
		cb(coderuntime.LSPEvent{Type: evType, Name: c.name, State: state, DiagnosticCount: total})
	}
}

// LSPStates returns the state of every server seen so far.
func (t *Toolbox) LSPStates() map[string]coderuntime.LSPClientInfo {
	for _, c := range t.m.Clients() {
		total := c.Counts().Total()
		t.mu.Lock()
		info, ok := t.states[c.name]
		if !ok || info.ConnectedAt.IsZero() {
			info = coderuntime.LSPClientInfo{Name: c.name, ConnectedAt: time.Now()}
		}
		info.State, info.DiagnosticCount = int(c.State()), total
		t.states[c.name] = info
		t.mu.Unlock()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]coderuntime.LSPClientInfo, len(t.states))
	for k, v := range t.states {
		out[k] = v
	}
	return out
}

// LSPDiagnostics returns the diagnostics one server published (a map of
// document URI to diagnostics), or an empty map.
func (t *Toolbox) LSPDiagnostics(name string) any {
	if c, ok := t.m.Client(name); ok {
		return c.Diagnostics()
	}
	return map[string]any{}
}

// StartLSP starts the servers that can handle path.
func (t *Toolbox) StartLSP(ctx context.Context, path string) { t.m.Start(ctx, path) }

// StopAllLSPs stops every running server.
func (t *Toolbox) StopAllLSPs(ctx context.Context) { t.m.StopAll(ctx) }

// SetLSPEventListener registers a listener for state and diagnostics changes.
func (t *Toolbox) SetLSPEventListener(fn func(e coderuntime.LSPEvent)) {
	t.mu.Lock()
	t.listener = fn
	t.mu.Unlock()
}

// Close stops background work and every server.
func (t *Toolbox) Close() {
	t.closeOnce.Do(func() {
		t.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		t.m.StopAll(ctx)
	})
}
