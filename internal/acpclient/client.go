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

	turnMu sync.Mutex // one prompt at a time

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
		AgentInfo   struct{ Name string } `json:"agentInfo"`
		AuthMethods []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"authMethods"`
	}
	_ = json.Unmarshal(raw, &init)
	c.agentName = init.AgentInfo.Name

	sid, err := c.newSession(ctx)
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) && rpcErr.Code == -32000 && len(init.AuthMethods) > 0 {
		// "Authentication required": use the first method the agent offers (usually its stored login
		// or an API key from the environment), then try again once.
		if _, aerr := c.rpc.call(ctx, "authenticate", map[string]any{"methodId": init.AuthMethods[0].ID}); aerr != nil {
			return fmt.Errorf("sign-in with %q failed: %w", init.AuthMethods[0].Name, aerr)
		}
		sid, err = c.newSession(ctx)
	}
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	c.sessionID = sid
	return nil
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
	turn := &Turn{Text: strings.TrimSpace(st.text.String()), Tools: st.tools, Denied: st.denied}
	c.mu.Unlock()
	if err != nil {
		if ctx.Err() != nil {
			turn.StopReason = "cancelled"
			return turn, nil
		}
		return turn, c.explain(err)
	}
	var res struct {
		StopReason string `json:"stopReason"`
	}
	_ = json.Unmarshal(raw, &res)
	turn.StopReason = res.StopReason
	return turn, nil
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
	if tail != "" && (errors.Is(err, ErrAgentExited) || strings.Contains(err.Error(), "initialize") || strings.Contains(err.Error(), "session/new")) {
		return fmt.Errorf("%s: %w\n--- its log ---\n%s", name, err, tail)
	}
	return fmt.Errorf("%s: %w", name, err)
}
