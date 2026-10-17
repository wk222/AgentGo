package codelsp

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	powernapconfig "github.com/charmbracelet/x/powernap/pkg/config"
	powernap "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/sourcegraph/jsonrpc2"
)

const unavailableRetryDelay = 30 * time.Second

// ServerSpec describes how to run and when to use one language server.
type ServerSpec struct {
	Command     string            `json:"command"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	FileTypes   []string          `json:"filetypes,omitempty"`
	RootMarkers []string          `json:"root_markers,omitempty"`
	InitOptions map[string]any    `json:"init_options,omitempty"`
	Settings    map[string]any    `json:"options,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	// TimeoutSec bounds the initialize handshake; default 30.
	TimeoutSec int `json:"timeout,omitempty"`
}

// Overrides are the user's server settings, keyed by server name. An entry
// with a known name replaces the bundled definition; an unknown name adds a
// server; Disabled removes one. User-configured servers start without the
// PATH/command checks that guard the bundled ones.
type Overrides map[string]ServerSpec

// LoadOverrides reads <dir>/lsp.json (a JSON object of name -> ServerSpec). A
// missing file is not an error.
func LoadOverrides(dir string) (Overrides, error) {
	b, err := os.ReadFile(filepath.Join(dir, "lsp.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var o Overrides
	if err := json.Unmarshal(b, &o); err != nil {
		return nil, err
	}
	return o, nil
}

// skipAutoStart lists launcher commands too generic to start without the user
// asking for them.
var skipAutoStart = map[string]bool{
	"buck2": true, "buf": true, "cue": true, "dart": true, "deno": true, "dotnet": true,
	"dprint": true, "gleam": true, "java": true, "julia": true, "koka": true, "node": true,
	"npx": true, "perl": true, "plz": true, "python": true, "python3": true, "R": true,
	"racket": true, "rome": true, "rubocop": true, "ruff": true, "scarb": true, "solc": true,
	"stylua": true, "swipl": true, "tflint": true,
}

// Manager starts language servers on demand, one per matching file type.
type Manager struct {
	workDir  string
	servers  map[string]ServerSpec
	user     map[string]bool
	autoLSP  bool
	callback func(name string, c *Client)

	mu          sync.Mutex
	clients     map[string]*Client
	unavailable map[string]time.Time

	now      func() time.Time
	lookPath func(string) (string, error)
}

// ManagerOptions configure NewManager.
type ManagerOptions struct {
	WorkDir   string
	Overrides Overrides
	// NoAuto stops bundled servers from starting on their own (only
	// user-configured ones start).
	NoAuto bool
}

// NewManager builds the server table: powernap's bundled definitions plus the
// user's overrides.
func NewManager(o ManagerOptions) *Manager {
	pm := powernapconfig.NewManager()
	pm.LoadDefaults()
	m := &Manager{
		workDir: o.WorkDir, servers: map[string]ServerSpec{}, user: map[string]bool{},
		autoLSP: !o.NoAuto, callback: func(string, *Client) {},
		clients: map[string]*Client{}, unavailable: map[string]time.Time{},
		now: time.Now, lookPath: exec.LookPath,
	}
	for name, s := range pm.GetServers() {
		m.servers[name] = ServerSpec{
			Command: s.Command, Args: s.Args, Env: s.Environment, FileTypes: s.FileTypes,
			RootMarkers: s.RootMarkers, InitOptions: s.InitOptions, Settings: s.Settings,
		}
	}
	for name, spec := range o.Overrides {
		// The user may have written the command where the name goes.
		actual := name
		if _, ok := m.servers[name]; !ok {
			for sn, s := range m.servers {
				if s.Command == name {
					actual = sn
					break
				}
			}
		}
		if spec.Disabled {
			delete(m.servers, actual)
			continue
		}
		m.servers[actual] = spec
		m.user[actual] = true
	}
	return m
}

// SetCallback is told whenever a client starts, changes state or stops.
func (m *Manager) SetCallback(cb func(name string, c *Client)) { m.callback = cb }

// Clients returns the running clients.
func (m *Manager) Clients() []*Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, c)
	}
	return out
}

// Client returns the named client.
func (m *Manager) Client(name string) (*Client, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clients[name]
	return c, ok
}

// FindClient returns a running client that handles path.
func (m *Manager) FindClient(path string) *Client {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	for _, c := range m.Clients() {
		if c.HandlesFile(path) {
			return c
		}
	}
	return nil
}

// Start starts every server that can handle path. It is a no-op for paths
// outside the workspace and for servers already running.
func (m *Manager) Start(ctx context.Context, path string) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if !within(m.workDir, path) {
		return
	}
	var wg sync.WaitGroup
	for name, spec := range m.servers {
		wg.Go(func() { m.startServer(name, path, spec) })
	}
	wg.Wait()
}

func running(s ServerState) bool {
	return s == StateReady || s == StateStarting || s == StateDisabled
}

func (m *Manager) existing(name string) (*Client, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clients[name]
	return c, ok && running(c.State())
}

func (m *Manager) startServer(name, path string, spec ServerSpec) {
	user := m.user[name]
	if !user && !m.autoLSP {
		return
	}
	if c, ok := m.existing(name); ok {
		// Another call is still bringing it up: wait, so the caller's query
		// does not hit a server that has not finished initialising.
		c.waitStarted(context.Background())
		m.callback(name, c)
		return
	}
	if user {
		if !handles(spec, path, m.workDir) {
			return
		}
	} else if !m.canAutoStart(name, path, spec) {
		return
	}

	client, err := newClient(name, spec, m.workDir)
	if err != nil {
		slog.Error("lsp: create client", "name", name, "error", err)
		return
	}
	m.mu.Lock()
	if old, ok := m.clients[name]; ok && running(old.State()) { // lost a race
		m.mu.Unlock()
		client.kill()
		old.waitStarted(context.Background())
		m.callback(name, old)
		return
	}
	m.clients[name] = client
	m.mu.Unlock()
	defer func() {
		client.markStarted()
		m.callback(name, client)
	}()

	client.setState(StateStarting)
	// Independent of the caller's context: a tool call may end long before the
	// server finishes starting, and the server must outlive it.
	initCtx, cancel := context.WithTimeout(context.Background(), time.Duration(cmp.Or(spec.TimeoutSec, 30))*time.Second)
	defer cancel()
	if err := client.initialize(initCtx); err != nil {
		slog.Error("lsp: initialize failed", "name", name, "error", err)
		client.kill()
		m.mu.Lock()
		delete(m.clients, name)
		m.mu.Unlock()
		return
	}
	if err := client.waitReady(initCtx); err != nil {
		slog.Warn("lsp: server not fully ready, continuing", "name", name, "error", err)
		client.setState(StateError)
	}
}

func (m *Manager) canAutoStart(name, path string, spec ServerSpec) bool {
	if skipAutoStart[spec.Command] {
		return false
	}
	// Cheap checks first: scanning PATH costs a stat per directory.
	if !handles(spec, path, m.workDir) {
		return false
	}
	m.mu.Lock()
	last, bad := m.unavailable[name]
	if bad && m.now().Sub(last) < unavailableRetryDelay {
		m.mu.Unlock()
		return false
	}
	m.mu.Unlock()
	if _, err := m.lookPath(spec.Command); err != nil {
		slog.Debug("lsp: server not installed", "name", name, "command", spec.Command)
		m.mu.Lock()
		m.unavailable[name] = m.now()
		m.mu.Unlock()
		return false
	}
	m.mu.Lock()
	delete(m.unavailable, name)
	m.mu.Unlock()
	return true
}

func handlesFiletype(fileTypes []string, path string) bool {
	if len(fileTypes) == 0 {
		return true
	}
	kind := powernap.DetectLanguage(path)
	name := strings.ToLower(filepath.Base(path))
	for _, ft := range fileTypes {
		suffix := strings.ToLower(ft)
		if !strings.HasPrefix(suffix, ".") {
			suffix = "." + suffix
		}
		if strings.HasSuffix(name, suffix) || ft == string(kind) {
			return true
		}
	}
	return false
}

// hasRootMarkers checks the workspace root only: walking a monorepo for
// markers is far more expensive than it is worth.
func hasRootMarkers(dir string, markers []string) bool {
	if len(markers) == 0 {
		return true
	}
	for _, pat := range markers {
		if matches, err := filepath.Glob(filepath.Join(dir, pat)); err == nil && len(matches) > 0 {
			return true
		}
	}
	return false
}

func handles(spec ServerSpec, path, workDir string) bool {
	return handlesFiletype(spec.FileTypes, path) && hasRootMarkers(workDir, spec.RootMarkers)
}

// StopAll shuts every server down gracefully.
func (m *Manager) StopAll(ctx context.Context) {
	m.mu.Lock()
	snapshot := m.clients
	m.clients = map[string]*Client{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for name, c := range snapshot {
		wg.Go(func() {
			defer func() { m.callback(name, c) }()
			if err := c.close(ctx); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, context.Canceled) &&
				!errors.Is(err, jsonrpc2.ErrClosed) && err.Error() != "signal: killed" {
				slog.Warn("lsp: stop", "name", name, "error", err)
			}
			c.cancelCtx()
			c.setState(StateStopped)
		})
	}
	wg.Wait()
}
