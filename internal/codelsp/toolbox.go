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
	// Search finds candidate positions for a symbol name; lsp_references,
	// lsp_definition and lsp_call_hierarchy start from them. Without it those
	// tools report that the symbol could not be located.
	Search SymbolSearch
}

// Toolbox is the coderuntime.Toolbox for the language-server tools. The file
// tools are not part of it; the caller layers them on top.
type Toolbox struct {
	sv    *supervisor
	tools []coderuntime.Tool
	root  string

	stopPriming context.CancelFunc
	closeOnce   sync.Once

	mu       sync.Mutex
	known    map[string]coderuntime.LSPClientInfo
	listener func(coderuntime.LSPEvent)
}

var _ coderuntime.Toolbox = (*Toolbox)(nil)

// New builds the toolbox. Servers start when a tool first touches a file they
// serve, and shortly after construction for the project's own manifest files.
func New(o Options) (*Toolbox, error) {
	if strings.TrimSpace(o.WorkDir) == "" {
		return nil, errors.New("codelsp: WorkDir is required")
	}
	root, err := filepath.Abs(o.WorkDir)
	if err != nil {
		return nil, err
	}
	ctx, stop := context.WithCancel(context.Background())
	t := &Toolbox{root: root, stopPriming: stop, known: map[string]coderuntime.LSPClientInfo{}}
	t.sv = newSupervisor(supervisorOptions{root: root, overrides: o.Overrides, noAuto: o.NoAuto, onChange: t.observe})
	t.tools = newTools(t.sv, root, o.Search)
	go t.prime(ctx)
	return t, nil
}

// prime starts the servers for whichever manifest files exist, so the first
// lsp_* call does not pay the startup cost.
func (t *Toolbox) prime(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for _, name := range []string{"go.mod", "main.go", "Cargo.toml", "package.json", "tsconfig.json", "pyproject.toml", "requirements.txt"} {
		p := filepath.Join(t.root, name)
		if _, err := os.Stat(p); err == nil {
			t.sv.ensure(ctx, p)
		}
	}
}

// Tools returns the lsp_* tools.
func (t *Toolbox) Tools() []coderuntime.Tool { return t.tools }

// record refreshes what is known about s and returns the new snapshot. The
// caller holds t.mu.
func (t *Toolbox) record(s *session) coderuntime.LSPClientInfo {
	info, ok := t.known[s.name]
	if !ok || info.ConnectedAt.IsZero() {
		info = coderuntime.LSPClientInfo{Name: s.name, ConnectedAt: time.Now()}
	}
	info.State = int(s.current())
	info.DiagnosticCount = s.diags.counts().total()
	t.known[s.name] = info
	return info
}

// observe is the supervisor's change hook: it updates the snapshot and tells the
// listener.
func (t *Toolbox) observe(s *session, kind string) {
	t.mu.Lock()
	info := t.record(s)
	listener := t.listener
	t.mu.Unlock()
	if listener != nil {
		listener(coderuntime.LSPEvent{Type: kind, Name: info.Name, State: info.State, DiagnosticCount: info.DiagnosticCount})
	}
}

// LSPStates reports every server seen so far, with live ones refreshed.
func (t *Toolbox) LSPStates() map[string]coderuntime.LSPClientInfo {
	live := t.sv.sessions()
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range live {
		t.record(s)
	}
	out := make(map[string]coderuntime.LSPClientInfo, len(t.known))
	for name, info := range t.known {
		out[name] = info
	}
	return out
}

// LSPDiagnostics returns what one server published (document URI to diagnostics),
// or an empty map when it is not running.
func (t *Toolbox) LSPDiagnostics(name string) any {
	if s, ok := t.sv.named(name); ok {
		return s.diags.snapshot()
	}
	return map[string]any{}
}

// StartLSP starts the servers that can handle path.
func (t *Toolbox) StartLSP(ctx context.Context, path string) { t.sv.ensure(ctx, path) }

// StopAllLSPs stops every running server.
func (t *Toolbox) StopAllLSPs(ctx context.Context) { t.sv.stopAll(ctx) }

// SetLSPEventListener registers the listener for state and diagnostics changes.
func (t *Toolbox) SetLSPEventListener(fn func(e coderuntime.LSPEvent)) {
	t.mu.Lock()
	t.listener = fn
	t.mu.Unlock()
}

// Close stops background work and every server.
func (t *Toolbox) Close() {
	t.closeOnce.Do(func() {
		t.stopPriming()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		t.sv.close(ctx)
	})
}
