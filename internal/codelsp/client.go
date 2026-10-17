// Package codelsp is AgentGo's native language-server layer for the code
// tools: server selection, a per-server client with a diagnostics cache, and
// the lsp_* tools. It is built straight on the powernap LSP transport and
// protocol types (github.com/charmbracelet/x/powernap, a standalone library),
// not on Crush; the selection rules and tool output formats are derived from
// Crush's lsp package and agent tools (third_party/crush, FSL-1.1-MIT).
package codelsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	powernap "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// ServerState is the lifecycle of one language server. The numbers are part of
// the contract with the UI (coderuntime.LSPClientInfo.State); keep them.
type ServerState int

const (
	StateUnstarted ServerState = iota
	StateStarting
	StateReady
	StateError
	StateStopped
	StateDisabled
)

// DiagnosticCounts counts diagnostics by severity.
type DiagnosticCounts struct{ Error, Warning, Information, Hint int }

// Total is the number of diagnostics of every severity.
func (c DiagnosticCounts) Total() int { return c.Error + c.Warning + c.Information + c.Hint }

type openFile struct {
	version int32
}

// Client is one running language server.
type Client struct {
	pc        *powernap.Client
	name      string
	cwd       string
	spec      ServerSpec
	ctx       context.Context
	cancelCtx context.CancelFunc

	state atomic.Int32
	// started is closed once the manager has finished bringing the server up
	// (or has given up on it). Callers that arrive while it is still starting
	// wait on it instead of querying a server that has not initialised.
	started     chan struct{}
	startedOnce sync.Once

	onDiagnostics func(name string, count int)

	dmu      sync.Mutex
	diags    map[protocol.DocumentURI][]protocol.Diagnostic
	dversion uint64

	omu  sync.Mutex
	open map[string]*openFile
}

func newClient(name string, spec ServerSpec, cwd string) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		name: name, cwd: cwd, spec: spec, ctx: ctx, cancelCtx: cancel, started: make(chan struct{}),
		diags: map[protocol.DocumentURI][]protocol.Diagnostic{},
		open:  map[string]*openFile{},
	}
	c.state.Store(int32(StateStopped))
	if err := c.createTransport(); err != nil {
		cancel()
		return nil, err
	}
	return c, nil
}

func (c *Client) createTransport() error {
	rootURI := string(protocol.URIFromPath(c.cwd))
	pc, err := powernap.NewClient(powernap.ClientConfig{
		Command:     expandHome(os.ExpandEnv(c.spec.Command)),
		Args:        expandAll(c.spec.Args),
		RootURI:     rootURI,
		Environment: expandEnvMap(c.spec.Env),
		Settings:    c.spec.Settings,
		InitOptions: c.spec.InitOptions,
		WorkspaceFolders: []protocol.WorkspaceFolder{
			{URI: rootURI, Name: filepath.Base(c.cwd)},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create lsp client: %w", err)
	}
	c.pc = pc
	return nil
}

func expandAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = os.ExpandEnv(s)
	}
	return out
}

func expandEnvMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = os.ExpandEnv(v)
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

func (c *Client) markStarted() { c.startedOnce.Do(func() { close(c.started) }) }

// waitStarted blocks until startup finished (successfully or not) or ctx ends.
func (c *Client) waitStarted(ctx context.Context) {
	select {
	case <-c.started:
	case <-ctx.Done():
	}
}

// Name is the server's name (for example "gopls").
func (c *Client) Name() string { return c.name }

// State reports the server's lifecycle state.
func (c *Client) State() ServerState { return ServerState(c.state.Load()) }

func (c *Client) setState(s ServerState) { c.state.Store(int32(s)) }

// SetDiagnosticsCallback is called with the server's total diagnostic count
// whenever it republishes.
func (c *Client) SetDiagnosticsCallback(fn func(name string, count int)) { c.onDiagnostics = fn }

// initialize performs the LSP handshake. Handlers are registered first because
// some servers send requests (window/workDoneProgress/create) during
// initialize and treat an unanswered one as fatal.
func (c *Client) initialize(ctx context.Context) error {
	c.registerHandlers()
	if err := c.pc.Initialize(ctx, false); err != nil {
		return fmt.Errorf("failed to initialize the lsp client: %w", err)
	}
	return nil
}

func (c *Client) registerHandlers() {
	c.pc.RegisterHandler("workspace/configuration", func(context.Context, string, json.RawMessage) (any, error) {
		return []map[string]any{{}}, nil
	})
	c.pc.RegisterHandler("client/registerCapability", func(context.Context, string, json.RawMessage) (any, error) {
		return nil, nil
	})
	c.pc.RegisterHandler("window/workDoneProgress/create", func(context.Context, string, json.RawMessage) (any, error) {
		return nil, nil
	})
	// We never advertise workspace edits, but answer the request so a server
	// that sends one anyway does not stall waiting for a reply.
	c.pc.RegisterHandler("workspace/applyEdit", func(context.Context, string, json.RawMessage) (any, error) {
		return protocol.ApplyWorkspaceEditResult{Applied: false, FailureReason: "client does not apply server edits"}, nil
	})
	c.pc.RegisterNotificationHandler("textDocument/publishDiagnostics", func(_ context.Context, _ string, params json.RawMessage) {
		var p protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			slog.Error("lsp: bad publishDiagnostics", "error", err)
			return
		}
		c.dmu.Lock()
		c.diags[p.URI] = p.Diagnostics
		c.dversion++
		total := 0
		for _, d := range c.diags {
			total += len(d)
		}
		c.dmu.Unlock()
		if fn := c.onDiagnostics; fn != nil {
			fn(c.name, total)
		}
	})
}

// waitReady blocks until the transport reports the server running.
func (c *Client) waitReady(ctx context.Context) error {
	c.setState(StateStarting)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	c.openRootMarkers(ctx)
	for {
		select {
		case <-ctx.Done():
			c.setState(StateError)
			return fmt.Errorf("timeout waiting for LSP server to be ready")
		case <-ticker.C:
			if c.pc.IsRunning() {
				c.setState(StateReady)
				return nil
			}
		}
	}
}

// openRootMarkers opens go.mod, package.json and friends: many servers only
// find the project once one of them is open.
func (c *Client) openRootMarkers(ctx context.Context) {
	for _, m := range c.spec.RootMarkers {
		p := filepath.Join(c.cwd, m)
		if _, err := os.Stat(p); err == nil {
			if err := c.openFile(ctx, p); err != nil {
				slog.Debug("lsp: open root marker", "file", p, "error", err)
			}
		}
	}
}

const closeTimeout = 5 * time.Second

// kill terminates the process without a handshake. The client is unusable after.
func (c *Client) kill() {
	c.cancelCtx()
	c.pc.Kill()
}

// close shuts the server down gracefully, falling back to a kill after
// closeTimeout (a hung server must not hang the app).
func (c *Client) close(ctx context.Context) error {
	c.closeAllFiles(ctx)
	closeCtx, cancel := context.WithTimeout(ctx, closeTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		if err := c.pc.Shutdown(closeCtx); err != nil {
			slog.Debug("lsp: shutdown", "name", c.name, "error", err)
		}
		done <- c.pc.Exit()
	}()
	select {
	case err := <-done:
		return err
	case <-closeCtx.Done():
		c.pc.Kill()
		return closeCtx.Err()
	}
}

// HandlesFile reports whether this server serves path (inside the workspace
// and a file type it handles).
func (c *Client) HandlesFile(path string) bool {
	if c == nil || !within(c.cwd, path) {
		return false
	}
	return handlesFiletype(c.spec.FileTypes, path)
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func uriOf(path string) string { return string(protocol.URIFromPath(path)) }

func (c *Client) openFile(ctx context.Context, path string) error {
	if !c.HandlesFile(path) {
		return nil
	}
	uri := uriOf(path)
	c.omu.Lock()
	_, exists := c.open[uri]
	c.omu.Unlock()
	if exists {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}
	if err := c.pc.NotifyDidOpenTextDocument(ctx, uri, string(powernap.DetectLanguage(path)), 1, string(content)); err != nil {
		return err
	}
	c.omu.Lock()
	c.open[uri] = &openFile{version: 1}
	c.omu.Unlock()
	return nil
}

// OpenOnDemand opens path in the server unless it already is.
func (c *Client) OpenOnDemand(ctx context.Context, path string) error {
	if c == nil {
		return nil
	}
	return c.openFile(ctx, path)
}

func (c *Client) sendFull(ctx context.Context, uri, path string, f *openFile) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}
	f.version++
	return c.pc.NotifyDidChangeTextDocument(ctx, uri, int(f.version), []protocol.TextDocumentContentChangeEvent{
		{Value: protocol.TextDocumentContentChangeWholeDocument{Text: string(content)}},
	})
}

// NotifyChange tells the server a file changed on disk.
func (c *Client) NotifyChange(ctx context.Context, path string) error {
	if c == nil {
		return nil
	}
	uri := uriOf(path)
	c.omu.Lock()
	f, ok := c.open[uri]
	c.omu.Unlock()
	if !ok {
		return fmt.Errorf("cannot notify change for unopened file: %s", path)
	}
	return c.sendFull(ctx, uri, path, f)
}

// RefreshOpenFiles re-sends every open file, prompting fresh diagnostics.
func (c *Client) RefreshOpenFiles(ctx context.Context) {
	if c == nil {
		return
	}
	c.omu.Lock()
	snapshot := make(map[string]*openFile, len(c.open))
	for k, v := range c.open {
		snapshot[k] = v
	}
	c.omu.Unlock()
	for uri, f := range snapshot {
		p, err := protocol.DocumentURI(uri).Path()
		if err != nil {
			continue
		}
		if err := c.sendFull(ctx, uri, p, f); err != nil {
			slog.Debug("lsp: refresh", "uri", uri, "error", err)
		}
	}
}

// NotifyWorkspaceChange nudges the server to re-analyse the whole project.
func (c *Client) NotifyWorkspaceChange(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.pc.NotifyDidChangeWatchedFiles(ctx, []protocol.FileEvent{
		{URI: protocol.DocumentURI(protocol.URIFromPath(c.cwd)), Type: protocol.Changed},
	})
}

func (c *Client) closeAllFiles(ctx context.Context) {
	c.omu.Lock()
	uris := make([]string, 0, len(c.open))
	for u := range c.open {
		uris = append(uris, u)
	}
	c.omu.Unlock()
	for _, u := range uris {
		if err := c.pc.NotifyDidCloseTextDocument(ctx, u); err != nil {
			continue
		}
		c.omu.Lock()
		delete(c.open, u)
		c.omu.Unlock()
	}
}

// Diagnostics returns a copy of every diagnostic the server published.
func (c *Client) Diagnostics() map[protocol.DocumentURI][]protocol.Diagnostic {
	if c == nil {
		return nil
	}
	c.dmu.Lock()
	defer c.dmu.Unlock()
	out := make(map[protocol.DocumentURI][]protocol.Diagnostic, len(c.diags))
	for k, v := range c.diags {
		out[k] = slices.Clone(v)
	}
	return out
}

func (c *Client) diagVersion() uint64 {
	c.dmu.Lock()
	defer c.dmu.Unlock()
	return c.dversion
}

// Counts returns the diagnostics per severity.
func (c *Client) Counts() DiagnosticCounts {
	var n DiagnosticCounts
	if c == nil {
		return n
	}
	c.dmu.Lock()
	defer c.dmu.Unlock()
	for _, ds := range c.diags {
		for _, d := range ds {
			switch d.Severity {
			case protocol.SeverityError:
				n.Error++
			case protocol.SeverityWarning:
				n.Warning++
			case protocol.SeverityInformation:
				n.Information++
			case protocol.SeverityHint:
				n.Hint++
			}
		}
	}
	return n
}

// WaitForDiagnostics waits until the server's diagnostics stop changing. If
// nothing arrives within a second it returns early: the server is not going to
// republish.
func (c *Client) WaitForDiagnostics(ctx context.Context, timeout time.Duration) {
	if c == nil {
		return
	}
	const (
		firstChange = time.Second
		settle      = 300 * time.Millisecond
	)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	first := time.NewTimer(min(timeout, firstChange))
	defer first.Stop()
	prev := c.diagVersion()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-first.C:
			return
		case <-tick.C:
			if c.diagVersion() != prev {
				c.settle(ctx, deadline.C, settle)
				return
			}
		}
	}
}

func (c *Client) settle(ctx context.Context, deadline <-chan time.Time, quiet time.Duration) {
	last := c.diagVersion()
	stable := time.Now()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			return
		case <-tick.C:
			if v := c.diagVersion(); v != last {
				last, stable = v, time.Now()
			} else if time.Since(stable) >= quiet {
				return
			}
		}
	}
}

// --- queries (line and character are 1-based, as the model sees them) --------

func (c *Client) query(ctx context.Context, path string, d time.Duration) (context.Context, context.CancelFunc, error) {
	if err := c.OpenOnDemand(ctx, path); err != nil {
		return nil, nil, err
	}
	qctx, cancel := context.WithTimeout(ctx, d)
	return qctx, cancel, nil
}

// FindReferences lists the references to the symbol at line:char.
func (c *Client) FindReferences(ctx context.Context, path string, line, char int, includeDecl bool) ([]protocol.Location, error) {
	ctx, cancel, err := c.query(ctx, path, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.pc.FindReferences(ctx, path, line-1, char-1, includeDecl)
}

// Definition returns where the symbol at line:char is defined.
func (c *Client) Definition(ctx context.Context, path string, line, char int) ([]protocol.Location, error) {
	ctx, cancel, err := c.query(ctx, path, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.pc.RequestDefinition(ctx, path, line-1, char-1)
}

// DocumentSymbols returns the outline of a file.
func (c *Client) DocumentSymbols(ctx context.Context, path string) ([]protocol.DocumentSymbolResult, error) {
	ctx, cancel, err := c.query(ctx, path, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.pc.RequestDocumentSymbols(ctx, path)
}

// PrepareCallHierarchy resolves the call-hierarchy item at line:char.
func (c *Client) PrepareCallHierarchy(ctx context.Context, path string, line, char int) ([]protocol.CallHierarchyItem, error) {
	ctx, cancel, err := c.query(ctx, path, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.pc.PrepareCallHierarchy(ctx, path, line-1, char-1)
}

// IncomingCalls returns the callers of item.
func (c *Client) IncomingCalls(ctx context.Context, item protocol.CallHierarchyItem) ([]protocol.CallHierarchyIncomingCall, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.pc.IncomingCalls(ctx, item)
}

// OutgoingCalls returns the callees of item.
func (c *Client) OutgoingCalls(ctx context.Context, item protocol.CallHierarchyItem) ([]protocol.CallHierarchyOutgoingCall, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return c.pc.OutgoingCalls(ctx, item)
}
