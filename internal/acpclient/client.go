// Package acpclient drives another coding agent over the Agent Client Protocol: it starts the
// agent as a child process (Claude Code, Codex, Gemini CLI, OpenCode, …), opens a session, sends
// prompts and collects the answer. AgentGo's own ACP server (internal/acp) is the other side of
// the same protocol; this package is the side an editor or a host plays.
package acpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// protocolVersion is the ACP major version this client speaks.
const protocolVersion = 1

// Config describes one agent to run.
type Config struct {
	Name       string     // shown in messages, e.g. "claude"
	Command    string     // shell command line that starts the agent's ACP server on stdio
	Cwd        string     // the agent's working directory
	Env        []string   // extra KEY=VALUE entries for the agent process
	Permission Permission // what the agent may do without asking us; the zero value is ReadOnly

	InitTimeout time.Duration // initialize + session/new; npx may download the agent first (default 90 s)

	// ResumeSessionID names an earlier session of this agent to carry on instead of starting a new
	// one (the SessionID of a previous Conn). It only works if the agent says it can load sessions;
	// when it cannot, or no longer knows that session, a new session starts and ResumeNote says why.
	ResumeSessionID string
}

// ToolCall is one tool use the agent reported during a turn.
type ToolCall struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind,omitempty"`
	Status string `json:"status,omitempty"`
}

// Turn is what one prompt produced.
type Turn struct {
	Text       string     `json:"text"`
	StopReason string     `json:"stop_reason"`
	Tools      []ToolCall `json:"tools,omitempty"`
	// Denied lists what the agent asked to do and was refused under the permission level.
	Denied []string `json:"denied,omitempty"`
	// SessionID is the agent's own id for the session; a later Config.ResumeSessionID can carry on it.
	SessionID string `json:"session_id,omitempty"`
	// Resumed and ResumeNote say how the session began and are set on its first turn only: Resumed
	// means the agent carried on an earlier session; ResumeNote says why a requested resume did not happen.
	Resumed    bool   `json:"resumed,omitempty"`
	ResumeNote string `json:"resume_note,omitempty"`
}

// Event is a streamed piece of a turn, for a caller that wants progress as it happens.
type Event struct {
	Kind string // "text" or "tool"
	Text string
	Tool ToolCall
}

// transport is the running agent: its streams and how to end it.
type transport struct {
	in     io.Reader
	out    io.Writer
	stderr func() string // the tail of what the agent logged
	close  func()        // kill the agent and everything it started
	wait   func()        // returns once the agent has been reaped
}

// Conn is an open session with one agent. Methods are safe for concurrent use; Prompt serializes
// turns.
type Conn struct {
	cfg       Config
	rpc       *rpcConn
	tr        transport
	sessionID string
	agentName string

	canLoad    bool   // the agent can load an earlier session (agentCapabilities.loadSession)
	resumed    bool   // this session carries on an earlier one
	resumeNote string // why a requested resume did not happen

	turnMu   sync.Mutex // one prompt at a time
	reported bool       // how the session began has gone out with a turn (guarded by turnMu)

	mu   sync.Mutex
	turn *turnState // the prompt in flight, if any
}

type turnState struct {
	text   strings.Builder
	tools  []ToolCall
	denied []string
	emit   func(Event)
}

// Launch starts the agent described by cfg and opens a session in cfg.Cwd.
func Launch(ctx context.Context, cfg Config) (*Conn, error) {
	tr, err := startProcess(cfg)
	if err != nil {
		return nil, err
	}
	return connect(ctx, cfg, tr)
}

// connect speaks the opening of the protocol over an already-running transport.
func connect(ctx context.Context, cfg Config, tr transport) (*Conn, error) {
	c := &Conn{cfg: cfg, tr: tr, rpc: newRPC(tr.in, tr.out)}
	c.rpc.onNotify = c.handleNotify
	c.rpc.onRequest = c.handleRequest

	timeout := cfg.InitTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ictx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.open(ictx); err != nil {
		c.Close()
		return nil, c.explain(err)
	}
	return c, nil
}

func (c *Conn) open(ctx context.Context) error {
	raw, err := c.rpc.call(ctx, "initialize", map[string]any{
		"protocolVersion":    protocolVersion,
		"clientCapabilities": map[string]any{}, // no fs/terminal: the agent uses its own tools in its own cwd
		"clientInfo":         map[string]any{"name": "agentgo", "title": "AgentGo", "version": "1"},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	var init struct {
		AgentInfo         struct{ Name string } `json:"agentInfo"`
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
		AuthMethods []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"authMethods"`
	}
	_ = json.Unmarshal(raw, &init)
	c.agentName = init.AgentInfo.Name
	c.canLoad = init.AgentCapabilities.LoadSession

	// "Authentication required" (code -32000): use the first method the agent offers (usually its
	// stored login or an API key from the environment), then try again once.
	signedIn := false
	canSignIn := func(err error) bool { return isAuthRequired(err) && len(init.AuthMethods) > 0 && !signedIn }
	signIn := func() error {
		signedIn = true
		if _, aerr := c.rpc.call(ctx, "authenticate", map[string]any{"methodId": init.AuthMethods[0].ID}); aerr != nil {
			return fmt.Errorf("sign-in with %q failed: %w", init.AuthMethods[0].Name, aerr)
		}
		return nil
	}

	if id := strings.TrimSpace(c.cfg.ResumeSessionID); id != "" {
		resumed, err := c.tryResume(ctx, id, canSignIn, signIn)
		if err != nil {
			return err
		}
		if resumed {
			return nil
		}
	}

	sid, err := c.newSession(ctx)
	if canSignIn(err) {
		if aerr := signIn(); aerr != nil {
			return aerr
		}
		sid, err = c.newSession(ctx)
	}
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	c.sessionID = sid
	return nil
}

func isAuthRequired(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == -32000
}

// tryResume asks the agent to load an earlier session. It reports whether that worked. When it
// did not because of the agent (it cannot load sessions, or no longer has this one) the answer is
// false with no error, and resumeNote says why: the caller then starts a new session. A failure
// that a new session would not fix (the agent died, the time ran out, sign-in failed) is an error.
func (c *Conn) tryResume(ctx context.Context, id string, canSignIn func(error) bool, signIn func() error) (bool, error) {
	if !c.canLoad {
		c.resumeNote = "the agent cannot load an earlier session"
		return false, nil
	}
	err := c.loadSession(ctx, id)
	if canSignIn(err) {
		if aerr := signIn(); aerr != nil {
			return false, aerr
		}
		err = c.loadSession(ctx, id)
	}
	var rpcErr *RPCError
	switch {
	case err == nil:
		c.sessionID, c.resumed = id, true
		return true, nil
	case errors.As(err, &rpcErr):
		c.resumeNote = fmt.Sprintf("the agent would not load session %s: %s", id, strings.TrimSpace(rpcErr.Message))
		return false, nil
	}
	return false, fmt.Errorf("session/load: %w", err)
}

// loadSession asks the agent to carry on an earlier session. The agent replays the conversation
// as session/update notifications before it answers; no turn is in flight yet, so handleNotify
// drops them and they cannot leak into the next answer.
func (c *Conn) loadSession(ctx context.Context, id string) error {
	_, err := c.rpc.call(ctx, "session/load", map[string]any{"sessionId": id, "cwd": c.cfg.Cwd, "mcpServers": []any{}})
	return err
}

func (c *Conn) newSession(ctx context.Context) (string, error) {
	raw, err := c.rpc.call(ctx, "session/new", map[string]any{"cwd": c.cfg.Cwd, "mcpServers": []any{}})
	if err != nil {
		return "", err
	}
	var res struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.SessionID == "" {
		return "", fmt.Errorf("the agent returned no session id")
	}
	return res.SessionID, nil
}

// AgentName is what the agent called itself in initialize ("" if it did not say).
func (c *Conn) AgentName() string { return c.agentName }

// SessionID is the agent's id for this session. Keep it to carry on the session later.
func (c *Conn) SessionID() string { return c.sessionID }

// CanResume reports whether the agent can load an earlier session, that is, whether keeping
// SessionID is worth anything.
func (c *Conn) CanResume() bool { return c.canLoad }

// Resumed reports whether this session carries on an earlier one (Config.ResumeSessionID).
func (c *Conn) Resumed() bool { return c.resumed }

// ResumeNote says why a requested resume did not happen ("" if none was requested, or it worked).
func (c *Conn) ResumeNote() string { return c.resumeNote }

// Alive reports whether the agent process is still talking to us.
func (c *Conn) Alive() bool {
	select {
	case <-c.rpc.done:
		return false
	default:
		return true
	}
}

// Prompt sends one user turn and waits for the agent to finish it. onEvent, if set, sees the
// answer and the tool calls as they stream in. Cancelling ctx asks the agent to stop first and
// kills it if it does not.
func (c *Conn) Prompt(ctx context.Context, text string, onEvent func(Event)) (*Turn, error) {
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	if !c.Alive() {
		return nil, c.explain(ErrAgentExited)
	}
	st := &turnState{emit: onEvent}
	c.mu.Lock()
	c.turn = st
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.turn = nil
		c.mu.Unlock()
	}()

	// The prompt call itself is not cut by ctx: on cancel the agent is asked to stop and answers
	// the call with stopReason "cancelled", which keeps whatever it had already written.
	pctx, stop := context.WithCancel(context.Background())
	defer stop()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = c.rpc.notify("session/cancel", map[string]any{"sessionId": c.sessionID})
			select {
			case <-time.After(10 * time.Second):
				c.Close() // it ignored the cancel
			case <-pctx.Done():
			}
		case <-pctx.Done():
		}
	}()

	raw, err := c.rpc.call(pctx, "session/prompt", map[string]any{
		"sessionId": c.sessionID,
		"prompt":    []map[string]any{{"type": "text", "text": text}},
	})
	stop()
	<-stopped
	c.mu.Lock()
	turn := &Turn{Text: strings.TrimSpace(st.text.String()), Tools: st.tools, Denied: st.denied, SessionID: c.sessionID}
	c.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			turn.StopReason = "cancelled"
			c.reportStart(turn)
			return turn, nil
		}
		return turn, c.explain(err)
	}
	var res struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(raw, &res)
	turn.StopReason = res.StopReason
	c.reportStart(turn)
	return turn, nil
}

// reportStart puts how the session began on the first turn that comes back. The caller holds turnMu.
func (c *Conn) reportStart(t *Turn) {
	if c.reported {
		return
	}
	c.reported = true
	t.Resumed, t.ResumeNote = c.resumed, c.resumeNote
}

// Close ends the agent and everything it started. It is safe to call more than once.
func (c *Conn) Close() {
	c.rpc.shutdown(ErrAgentExited)
	if c.tr.close != nil {
		c.tr.close()
	}
	if c.tr.wait != nil {
		c.tr.wait()
	}
}

// explain adds what the agent logged on stderr to an error that says it died, since "the process
// ended" alone never says why (a missing API key, an unsupported flag, a crash).
func (c *Conn) explain(err error) error {
	if err == nil {
		return nil
	}
	name := c.cfg.Name
	if name == "" {
		name = "the agent"
	}
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return fmt.Errorf("%s: %w", name, err)
	}
	tail := ""
	if c.tr.stderr != nil {
		tail = strings.TrimSpace(c.tr.stderr())
	}
	if tail != "" && (errors.Is(err, ErrAgentExited) || strings.Contains(err.Error(), "initialize") ||
		strings.Contains(err.Error(), "session/new") || strings.Contains(err.Error(), "session/load")) {
		return fmt.Errorf("%s: %w\n--- its log ---\n%s", name, err, tail)
	}
	return fmt.Errorf("%s: %w", name, err)
}
