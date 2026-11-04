package acpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
)

// fakeAgent is an ACP agent living in the test process, reachable through a transport.
type fakeAgent struct {
	t *testing.T

	// behaviour
	authRequired bool                                                // session/new fails with -32000 until authenticate is called
	onPrompt     func(a *fakeAgent, id json.RawMessage, text string) // default: say "ok"

	mu           sync.Mutex
	out          io.Writer // agent → client
	authed       bool
	prompts      []string
	cancels      int
	permOutcomes []string
	cwd          string
	nextReq      int
	waiting      map[int]chan json.RawMessage // our requests to the client, by id
	exited       chan struct{}
	cancelled    chan struct{} // gets a token for every session/cancel
}

func newFake(t *testing.T) *fakeAgent {
	return &fakeAgent{t: t, waiting: map[int]chan json.RawMessage{}, exited: make(chan struct{}), cancelled: make(chan struct{}, 4)}
}

// transport wires a client to the agent through two pipes. closed ends the agent's side.
func (a *fakeAgent) transport() transport {
	toAgentR, toAgentW := io.Pipe()
	toClientR, toClientW := io.Pipe()
	a.out = toClientW
	go a.serve(toAgentR)
	var once sync.Once
	return transport{
		in: toClientR, out: toAgentW,
		stderr: func() string { return "fake agent log: boom" },
		close: func() {
			once.Do(func() {
				_ = toAgentW.Close()
				_ = toClientW.Close()
			})
		},
		wait: func() { <-a.exited },
	}
}

func (a *fakeAgent) serve(in io.Reader) {
	defer close(a.exited)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var m message
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			go a.handle(m)
		case m.Method == "session/cancel":
			a.mu.Lock()
			a.cancels++
			a.mu.Unlock()
			select {
			case a.cancelled <- struct{}{}:
			default:
			}
		case m.Method == "":
			a.deliver(m)
		}
	}
}

func (a *fakeAgent) deliver(m message) {
	var id int
	_ = json.Unmarshal(m.ID, &id)
	a.mu.Lock()
	ch := a.waiting[id]
	delete(a.waiting, id)
	a.mu.Unlock()
	if ch != nil {
		if m.Error != nil {
			ch <- json.RawMessage(`{"error":true}`)
		} else {
			ch <- m.Result
		}
	}
}

func (a *fakeAgent) write(v any) {
	b, _ := json.Marshal(v)
	b = append(b, '\n')
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.out.Write(b)
}

func (a *fakeAgent) reply(id json.RawMessage, result any) {
	a.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (a *fakeAgent) fail(id json.RawMessage, code int, msg string) {
	a.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

// update streams a session/update notification.
func (a *fakeAgent) update(u map[string]any) {
	a.write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "s1", "update": u}})
}

func (a *fakeAgent) say(text string) {
	a.update(map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": text}})
}

// askPermission makes a request of the client and waits for its answer, returning the chosen option id.
func (a *fakeAgent) askPermission(kind, title string) string {
	a.mu.Lock()
	a.nextReq++
	id := 1000 + a.nextReq
	ch := make(chan json.RawMessage, 1)
	a.waiting[id] = ch
	a.mu.Unlock()
	a.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": "session/request_permission", "params": map[string]any{
		"sessionId": "s1",
		"toolCall":  map[string]any{"toolCallId": "t-" + kind, "title": title, "kind": kind},
		"options": []map[string]any{
			{"optionId": "yes", "name": "Allow", "kind": "allow_once"},
			{"optionId": "always", "name": "Always", "kind": "allow_always"},
			{"optionId": "no", "name": "Reject", "kind": "reject_once"},
		},
	}})
	res := <-ch
	var out struct {
		Outcome struct {
			Outcome  string `json:"outcome"`
			OptionID string `json:"optionId"`
		} `json:"outcome"`
	}
	_ = json.Unmarshal(res, &out)
	got := out.Outcome.OptionID
	if out.Outcome.Outcome == "cancelled" {
		got = "cancelled"
	}
	a.mu.Lock()
	a.permOutcomes = append(a.permOutcomes, got)
	a.mu.Unlock()
	return got
}

func (a *fakeAgent) handle(m message) {
	switch m.Method {
	case "initialize":
		auth := []map[string]any{}
		if a.authRequired {
			auth = append(auth, map[string]any{"id": "login", "name": "Login"})
		}
		a.reply(m.ID, map[string]any{"protocolVersion": 1, "agentInfo": map[string]any{"name": "fake-agent"}, "authMethods": auth})
	case "authenticate":
		a.mu.Lock()
		a.authed = true
		a.mu.Unlock()
		a.reply(m.ID, map[string]any{})
	case "session/new":
		var p struct{ Cwd string }
		_ = json.Unmarshal(m.Params, &p)
		a.mu.Lock()
		a.cwd = p.Cwd
		authed := a.authed
		a.mu.Unlock()
		if a.authRequired && !authed {
			a.fail(m.ID, -32000, "Authentication required")
			return
		}
		a.reply(m.ID, map[string]any{"sessionId": "s1"})
	case "session/prompt":
		var p struct {
			Prompt []struct{ Text string } `json:"prompt"`
		}
		_ = json.Unmarshal(m.Params, &p)
		text := ""
		if len(p.Prompt) > 0 {
			text = p.Prompt[0].Text
		}
		a.mu.Lock()
		a.prompts = append(a.prompts, text)
		a.mu.Unlock()
		if a.onPrompt != nil {
			a.onPrompt(a, m.ID, text)
			return
		}
		a.say("ok")
		a.reply(m.ID, map[string]any{"stopReason": "end_turn"})
	default:
		a.fail(m.ID, -32601, "method not found")
	}
}

func launchFake(t *testing.T, a *fakeAgent, perm Permission) *Conn {
	t.Helper()
	c, err := connect(context.Background(), Config{Name: "fake", Cwd: "/work", Permission: perm}, a.transport())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}
