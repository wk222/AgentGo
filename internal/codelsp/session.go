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

	powernap "agentgo/internal/powernaplsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// ServerState is where a language server is in its life. The numbers are part of
// the contract with the UI (coderuntime.LSPClientInfo.State); do not renumber.
type ServerState int

const (
	StateUnstarted ServerState = iota
	StateStarting
	StateReady
	StateError
	StateStopped
	StateDisabled
)

// severityCounts tallies diagnostics by severity.
type severityCounts struct{ errors, warnings, infos, hints int }

func (c severityCounts) total() int { return c.errors + c.warnings + c.infos + c.hints }

// --- diagnostics cache ---------------------------------------------------------

// diagStore holds the latest diagnostics each document received. Every change
// bumps a revision, which lets a waiter see "the server said something new"
// without comparing contents.
type diagStore struct {
	mu    sync.Mutex
	byURI map[protocol.DocumentURI][]protocol.Diagnostic
	rev   uint64
}

// replace stores a document's new list and returns the total now held.
func (d *diagStore) replace(uri protocol.DocumentURI, list []protocol.Diagnostic) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byURI == nil {
		d.byURI = map[protocol.DocumentURI][]protocol.Diagnostic{}
	}
	d.byURI[uri] = list
	d.rev++
	n := 0
	for _, l := range d.byURI {
		n += len(l)
	}
	return n
}

func (d *diagStore) revision() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rev
}

func (d *diagStore) snapshot() map[protocol.DocumentURI][]protocol.Diagnostic {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[protocol.DocumentURI][]protocol.Diagnostic, len(d.byURI))
	for uri, l := range d.byURI {
		out[uri] = slices.Clone(l)
	}
	return out
}

func (d *diagStore) counts() severityCounts {
	d.mu.Lock()
	defer d.mu.Unlock()
	var c severityCounts
	for _, l := range d.byURI {
		for _, diag := range l {
			switch diag.Severity {
			case protocol.SeverityError:
				c.errors++
			case protocol.SeverityWarning:
				c.warnings++
			case protocol.SeverityInformation:
				c.infos++
			case protocol.SeverityHint:
				c.hints++
			}
		}
	}
	return c
}

// --- open documents ------------------------------------------------------------

// docSet tracks which documents the server has been told about and the version
// number of each, as the protocol requires for didChange.
type docSet struct {
	mu       sync.Mutex
	versions map[string]int32
}

func (d *docSet) has(uri string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.versions[uri]
	return ok
}

func (d *docSet) add(uri string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.versions == nil {
		d.versions = map[string]int32{}
	}
	d.versions[uri] = 1
}

// bump advances a document's version and returns the new one; ok is false for a
// document that was never opened.
func (d *docSet) bump(uri string) (v int32, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.versions[uri]; !ok {
		return 0, false
	}
	d.versions[uri]++
	return d.versions[uri], true
}

func (d *docSet) uris() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.versions))
	for u := range d.versions {
		out = append(out, u)
	}
	return out
}

func (d *docSet) drop(uri string) {
	d.mu.Lock()
	delete(d.versions, uri)
	d.mu.Unlock()
}

// --- session -------------------------------------------------------------------

const (
	queryTimeout    = 5 * time.Second
	hierarchyBudget = 10 * time.Second
	shutdownBudget  = 5 * time.Second
)

// session is one running language server and everything known about it.
type session struct {
	name string
	root string
	spec ServerSpec

	link   *powernap.Client
	ctx    context.Context
	cancel context.CancelFunc

	state atomic.Int32
	// settled closes once bring-up has finished, successfully or not. A tool call
	// that finds the server still starting waits here instead of querying a server
	// that has not initialised.
	settled     chan struct{}
	settledOnce sync.Once

	diags   diagStore
	docs    docSet
	changed func(s *session, kind string) // "state_changed" | "diagnostics_changed"
}

func newSession(name string, spec ServerSpec, root string, changed func(*session, string)) (*session, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{name: name, root: root, spec: spec, ctx: ctx, cancel: cancel, settled: make(chan struct{}), changed: changed}
	s.state.Store(int32(StateStopped))

	rootURI := string(protocol.URIFromPath(root))
	link, err := powernap.NewClient(powernap.ClientConfig{
		Command:     expandHome(os.ExpandEnv(spec.Command)),
		Args:        expandEach(spec.Args),
		RootURI:     rootURI,
		Environment: expandEnv(spec.Env),
		Settings:    spec.Settings,
		InitOptions: spec.InitOptions,
		WorkspaceFolders: []protocol.WorkspaceFolder{
			{URI: rootURI, Name: filepath.Base(root)},
		},
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("cannot create the %s language-server client: %w", name, err)
	}
	s.link = link
	return s, nil
}

func expandEach(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = os.ExpandEnv(v)
	}
	return out
}

func expandEnv(in map[string]string) map[string]string {
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

func (s *session) setState(st ServerState) { s.state.Store(int32(st)) }
func (s *session) current() ServerState    { return ServerState(s.state.Load()) }
func (s *session) markSettled()            { s.settledOnce.Do(func() { close(s.settled) }) }

// awaitSettled blocks until bring-up is over or ctx ends.
func (s *session) awaitSettled(ctx context.Context) {
	select {
	case <-s.settled:
	case <-ctx.Done():
	}
}

func (s *session) serves(path string) bool {
	return s != nil && within(s.root, path) && matchesFileType(s.spec.FileTypes, path)
}

// handshake runs the LSP initialize exchange. Handlers go in first because some
// servers send requests of their own during initialize (window/workDoneProgress/
// create) and treat an unanswered one as fatal.
func (s *session) handshake(ctx context.Context) error {
	s.installHandlers()
	if err := s.link.Initialize(ctx, false); err != nil {
		return fmt.Errorf("initialize failed: %w", err)
	}
	return nil
}

func (s *session) installHandlers() {
	reply := func(v any) func(context.Context, string, json.RawMessage) (any, error) {
		return func(context.Context, string, json.RawMessage) (any, error) { return v, nil }
	}
	s.link.RegisterHandler("workspace/configuration", reply([]map[string]any{{}}))
	s.link.RegisterHandler("client/registerCapability", reply(nil))
	s.link.RegisterHandler("window/workDoneProgress/create", reply(nil))
	// Edits are never advertised, but a server that sends one anyway must get an
	// answer or it may stall.
	s.link.RegisterHandler("workspace/applyEdit", reply(protocol.ApplyWorkspaceEditResult{
		Applied: false, FailureReason: "this client does not apply server-initiated edits",
	}))
	s.link.RegisterNotificationHandler("textDocument/publishDiagnostics", func(_ context.Context, _ string, params json.RawMessage) {
		var p protocol.PublishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			slog.Error("lsp: undecodable publishDiagnostics", "server", s.name, "error", err)
			return
		}
		s.diags.replace(p.URI, p.Diagnostics)
		if s.changed != nil {
			s.changed(s, "diagnostics_changed")
		}
	})
}

// awaitRunning waits for the transport to report the server alive, opening the
// project's marker files meanwhile (many servers only find the project once one
// of them is open).
func (s *session) awaitRunning(ctx context.Context) error {
	s.setState(StateStarting)
	s.openMarkers(ctx)
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if s.link.IsRunning() {
			s.setState(StateReady)
			return nil
		}
		select {
		case <-ctx.Done():
			s.setState(StateError)
			return fmt.Errorf("the %s server did not become ready in time", s.name)
		case <-tick.C:
		}
	}
}

func (s *session) openMarkers(ctx context.Context) {
	for _, m := range s.spec.RootMarkers {
		p := filepath.Join(s.root, m)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if err := s.open(ctx, p); err != nil {
			slog.Debug("lsp: could not open marker file", "file", p, "error", err)
		}
	}
}

// kill ends the process without a handshake; the session is unusable afterwards.
func (s *session) kill() {
	s.cancel()
	s.link.Kill()
}

// shutdown stops the server politely and falls back to killing it after
// shutdownBudget, so a hung server cannot hang the application.
func (s *session) shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, shutdownBudget)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		s.closeDocs(ctx)
		if err := s.link.Shutdown(ctx); err != nil {
			slog.Debug("lsp: shutdown request failed", "server", s.name, "error", err)
		}
		done <- s.link.Exit()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		s.link.Kill()
		return ctx.Err()
	}
}

// --- document sync -------------------------------------------------------------

func uriFor(path string) string { return string(protocol.URIFromPath(path)) }

// open tells the server about a file it has not seen yet.
func (s *session) open(ctx context.Context, path string) error {
	if !s.serves(path) {
		return nil
	}
	uri := uriFor(path)
	if s.docs.has(uri) {
		return nil
	}
	text, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	lang := string(powernap.DetectLanguage(path))
	if err := s.link.NotifyDidOpenTextDocument(ctx, uri, lang, 1, string(text)); err != nil {
		return err
	}
	s.docs.add(uri)
	return nil
}

// resend sends a document's current contents from disk as a full replacement.
func (s *session) resend(ctx context.Context, uri, path string) error {
	text, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", path, err)
	}
	v, ok := s.docs.bump(uri)
	if !ok {
		return fmt.Errorf("%s was never opened in %s", path, s.name)
	}
	return s.link.NotifyDidChangeTextDocument(ctx, uri, int(v), []protocol.TextDocumentContentChangeEvent{
		{Value: protocol.TextDocumentContentChangeWholeDocument{Text: string(text)}},
	})
}

// refresh tells the server that a file changed on disk.
func (s *session) refresh(ctx context.Context, path string) error {
	return s.resend(ctx, uriFor(path), path)
}

// refreshAll re-sends every open document, which prompts fresh diagnostics.
func (s *session) refreshAll(ctx context.Context) {
	for _, uri := range s.docs.uris() {
		p, err := protocol.DocumentURI(uri).Path()
		if err != nil {
			continue
		}
		if err := s.resend(ctx, uri, p); err != nil {
			slog.Debug("lsp: could not refresh", "uri", uri, "error", err)
		}
	}
}

// nudgeWorkspace asks the server to look at the whole project again.
func (s *session) nudgeWorkspace(ctx context.Context) error {
	return s.link.NotifyDidChangeWatchedFiles(ctx, []protocol.FileEvent{
		{URI: protocol.DocumentURI(protocol.URIFromPath(s.root)), Type: protocol.Changed},
	})
}

func (s *session) closeDocs(ctx context.Context) {
	for _, uri := range s.docs.uris() {
		if err := s.link.NotifyDidCloseTextDocument(ctx, uri); err == nil {
			s.docs.drop(uri)
		}
	}
}

// awaitDiagnostics returns once the server's diagnostics have stopped changing.
// If nothing new arrives within the first second the server is not going to say
// anything, so it returns early rather than burning the whole budget.
func (s *session) awaitDiagnostics(ctx context.Context, budget time.Duration) {
	const (
		firstWindow = time.Second
		quiet       = 300 * time.Millisecond
	)
	begin := time.Now()
	last := s.diags.revision()
	var lastChange time.Time
	moved := false
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		elapsed := time.Since(begin)
		if elapsed >= budget {
			return
		}
		if rev := s.diags.revision(); rev != last {
			last, lastChange, moved = rev, time.Now(), true
			continue
		}
		if !moved && elapsed >= min(budget, firstWindow) {
			return
		}
		if moved && time.Since(lastChange) >= quiet {
			return
		}
	}
}

// --- queries --------------------------------------------------------------------
// Lines and columns here are 1-based, as the model writes them; the protocol is
// 0-based, so each call converts.

func (s *session) prepare(ctx context.Context, path string, limit time.Duration) (context.Context, context.CancelFunc, error) {
	if err := s.open(ctx, path); err != nil {
		return nil, nil, err
	}
	qctx, cancel := context.WithTimeout(ctx, limit)
	return qctx, cancel, nil
}

func (s *session) references(ctx context.Context, path string, line, col int) ([]protocol.Location, error) {
	ctx, cancel, err := s.prepare(ctx, path, queryTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return s.link.FindReferences(ctx, path, line-1, col-1, true)
}

func (s *session) definition(ctx context.Context, path string, line, col int) ([]protocol.Location, error) {
	ctx, cancel, err := s.prepare(ctx, path, queryTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return s.link.RequestDefinition(ctx, path, line-1, col-1)
}

func (s *session) outline(ctx context.Context, path string) ([]protocol.DocumentSymbolResult, error) {
	ctx, cancel, err := s.prepare(ctx, path, queryTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return s.link.RequestDocumentSymbols(ctx, path)
}

func (s *session) hierarchyItems(ctx context.Context, path string, line, col int) ([]protocol.CallHierarchyItem, error) {
	ctx, cancel, err := s.prepare(ctx, path, queryTimeout)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return s.link.PrepareCallHierarchy(ctx, path, line-1, col-1)
}

func (s *session) callers(ctx context.Context, item protocol.CallHierarchyItem) ([]protocol.CallHierarchyIncomingCall, error) {
	ctx, cancel := context.WithTimeout(ctx, hierarchyBudget)
	defer cancel()
	return s.link.IncomingCalls(ctx, item)
}

func (s *session) callees(ctx context.Context, item protocol.CallHierarchyItem) ([]protocol.CallHierarchyOutgoingCall, error) {
	ctx, cancel := context.WithTimeout(ctx, hierarchyBudget)
	defer cancel()
	return s.link.OutgoingCalls(ctx, item)
}
