// Package crushengine implements engine.AgentEngine on top of Charm's Crush
// (run as a sidecar `crush server`, driven over its /v1 HTTP+SSE API).
//
// Crush owns the coding loop (tools, LSP, edit/diff, summarization); AgentGo
// owns orchestration, memory and governance. No Crush source is imported, so
// Crush's `internal/` restriction and FSL licensing stay out of AgentGo's tree.
package crushengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentgo/internal/engine"
)

// EngineID is the id this engine registers under.
const EngineID = "crush"

// PermissionRequest is what an Approver is asked to decide.
type PermissionRequest struct {
	ID, SessionID, ToolName, Description, Action, Path string
	Params                                             json.RawMessage
}

// Approver decides a Crush tool permission; return true to allow once.
// It runs in its own goroutine and may block (e.g. waiting for a human);
// ctx is cancelled when the run ends.
type Approver func(ctx context.Context, p PermissionRequest) bool

// Config configures the engine.
type Config struct {
	// Exe is the crush executable. Ignored when ServerAddr is set.
	Exe string
	// ServerAddr attaches to an already running `crush server` (host:port, TCP)
	// instead of spawning one. The engine never stops an external server.
	ServerAddr string
	// ConfigDir becomes CRUSH_GLOBAL_CONFIG (holds crush.json: providers/models/mcp).
	ConfigDir string
	// ExtraEnv entries (KEY=VALUE) added to the server environment.
	ExtraEnv []string
	// DataDir is passed to `crush server --data-dir`.
	DataDir string
	// LogPath receives the server's stdout/stderr.
	LogPath string
	// DefaultWorkspace is used when RunRequest.Workspace is empty.
	DefaultWorkspace string
	// AutoApprove allows every tool permission (no human in the loop).
	// Ignored for requests when Approver is set.
	AutoApprove bool
	Approver    Approver
}

// Engine is a engine.AgentEngine backed by a Crush sidecar.
type Engine struct {
	cfg      Config
	sup      *Supervisor
	clientID string

	mu         sync.Mutex
	client     *Client
	gen        int
	workspaces map[string]string // abs path -> workspace id
	sessions   map[string]string // wsID\x00agentgoSession -> crush session id
}

func New(cfg Config) *Engine {
	e := &Engine{
		cfg: cfg, clientID: newUUID(),
		workspaces: map[string]string{}, sessions: map[string]string{},
	}
	if cfg.ServerAddr == "" {
		env := append([]string(nil), cfg.ExtraEnv...)
		if cfg.ConfigDir != "" {
			env = append(env, "CRUSH_GLOBAL_CONFIG="+cfg.ConfigDir)
		}
		// Providers are resolved from the config we manage; don't hit the network.
		env = append(env, "CRUSH_DISABLE_PROVIDER_AUTO_UPDATE=1")
		e.sup = &Supervisor{Exe: cfg.Exe, Env: env, DataDir: cfg.DataDir, LogPath: cfg.LogPath}
	}
	return e
}

func (e *Engine) ID() string { return EngineID }

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// connect returns a client for a live server, resetting caches if the server
// was (re)started since the last call.
func (e *Engine) connect(ctx context.Context) (*Client, error) {
	addr, gen := e.cfg.ServerAddr, 1
	if e.sup != nil {
		var err error
		if addr, gen, err = e.sup.Ensure(ctx); err != nil {
			return nil, err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client == nil || e.gen != gen {
		e.client, e.gen = NewClient(addr, e.clientID), gen
		e.workspaces = map[string]string{}
		e.sessions = map[string]string{}
	}
	return e.client, nil
}

func (e *Engine) workspaceID(ctx context.Context, cl *Client, path string) (string, error) {
	e.mu.Lock()
	id, ok := e.workspaces[path]
	e.mu.Unlock()
	if ok {
		return id, nil
	}
	ws, err := cl.CreateWorkspace(ctx, path, e.cfg.AutoApprove && e.cfg.Approver == nil)
	if err != nil {
		return "", fmt.Errorf("create crush workspace: %w", err)
	}
	e.mu.Lock()
	e.workspaces[path] = ws.ID
	e.mu.Unlock()
	return ws.ID, nil
}

func (e *Engine) sessionID(ctx context.Context, cl *Client, wsID, agentgoSession string) (string, error) {
	key := wsID + "\x00" + agentgoSession
	if agentgoSession != "" {
		e.mu.Lock()
		id, ok := e.sessions[key]
		e.mu.Unlock()
		if ok {
			return id, nil
		}
	}
	title := "AgentGo"
	if agentgoSession != "" {
		title = "AgentGo: " + agentgoSession
	}
	s, err := cl.CreateSession(ctx, wsID, title)
	if err != nil {
		return "", fmt.Errorf("create crush session: %w", err)
	}
	if agentgoSession != "" {
		e.mu.Lock()
		e.sessions[key] = s.ID
		e.mu.Unlock()
	}
	return s.ID, nil
}

// Close detaches from Crush and stops the sidecar if the engine started it.
func (e *Engine) Close() {
	e.mu.Lock()
	cl, ids := e.client, make([]string, 0, len(e.workspaces))
	for _, id := range e.workspaces {
		ids = append(ids, id)
	}
	e.workspaces, e.sessions = map[string]string{}, map[string]string{}
	e.mu.Unlock()
	if cl != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		for _, id := range ids {
			_ = cl.DeleteWorkspace(ctx, id)
		}
		cancel()
	}
	if e.sup != nil {
		e.sup.Stop()
	}
}

// Run executes one coding turn in Crush and streams its progress.
func (e *Engine) Run(ctx context.Context, req engine.RunRequest, em engine.Emitter) (engine.RunResult, error) {
	path := req.Workspace
	if path == "" {
		path = e.cfg.DefaultWorkspace
	}
	if path == "" {
		return engine.RunResult{}, errors.New("crush engine: no workspace (set RunRequest.Workspace or Config.DefaultWorkspace)")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return engine.RunResult{}, err
	}
	if strings.TrimSpace(req.Input) == "" {
		return engine.RunResult{}, errors.New("crush engine: empty input")
	}

	cl, err := e.connect(ctx)
	if err != nil {
		return engine.RunResult{}, err
	}
	wsID, err := e.workspaceID(ctx, cl, path)
	if err != nil {
		return engine.RunResult{}, err
	}
	sid, err := e.sessionID(ctx, cl, wsID, req.SessionID)
	if err != nil {
		return engine.RunResult{}, err
	}
	runID := req.RunID
	if runID == "" {
		runID = newUUID()
	}
	return e.drive(ctx, cl, wsID, sid, runID, req.Input, em)
}
