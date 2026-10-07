package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/crushproto"
	"agentgo/internal/engine"
)

// This file checks the cross-surface path through the real pieces: a Crush TUI
// client talking HTTP to crushproto, the Crush runner, the approval queue and
// the desktop binding. Only the LLM resume is scripted.

type handoffRunner struct {
	r          *crushRunner
	approvalID string
}

func (h handoffRunner) Run(ctx context.Context, req crushproto.RunRequest, em *crushproto.Emitter) error {
	em.UserMessage(req.Prompt)
	t := newCrushTurn(em)
	h.r.approve(ctx, em, t, engine.Message{
		Role: "assistant", Type: "approval", ApprovalID: h.approvalID, ToolName: "code_edit",
		Arguments: `{"path":"a.go"}`, Status: "pending", Content: "Approve code_edit",
	})
	t.finish()
	em.Complete(req, t.lastID, t.lastText, "t", 0, 0)
	return nil
}

// tuiClient is the part of the stock Crush TUI that matters here.
type tuiClient struct {
	t      *testing.T
	base   string
	ws     string
	frames chan string
	closes []func()
}

func newTUIClient(t *testing.T, base string) *tuiClient {
	t.Helper()
	c := &tuiClient{t: t, base: base}
	var ws map[string]any
	c.post("/v1/workspaces", map[string]any{"path": t.TempDir(), "data_dir": t.TempDir(), "client_id": "tui", "env": []string{}}, &ws)
	c.ws, _ = ws["id"].(string)
	if c.ws == "" {
		t.Fatalf("no workspace: %v", ws)
	}
	t.Cleanup(func() {
		for _, f := range c.closes {
			f()
		}
	})
	return c
}

func (c *tuiClient) post(path string, body, out any) int {
	c.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := http.Post(c.base+path, "application/json", bytes.NewReader(b))
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

// connect opens an event stream and forwards frames to c.frames.
func (c *tuiClient) connect() (disconnect func()) {
	c.t.Helper()
	resp, err := http.Get(c.base + "/v1/workspaces/" + c.ws + "/events")
	if err != nil {
		c.t.Fatal(err)
	}
	c.frames = make(chan string, 512)
	frames := c.frames
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				frames <- sc.Text()[6:]
			}
		}
	}()
	d := func() { resp.Body.Close() }
	c.closes = append(c.closes, d)
	return d
}

// waitFrame returns the first frame (from now on) containing every needle.
func (c *tuiClient) waitFrame(needles ...string) string {
	c.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-c.frames:
			ok := true
			for _, n := range needles {
				ok = ok && strings.Contains(f, n)
			}
			if ok {
				return f
			}
		case <-deadline:
			c.t.Fatalf("no frame containing %q", needles)
		}
	}
}

func (c *tuiClient) startRun(prompt string) (sessionID string) {
	c.t.Helper()
	var s crushproto.Session
	c.post("/v1/workspaces/"+c.ws+"/sessions", crushproto.Session{Title: "non-interactive"}, &s)
	if code := c.post("/v1/workspaces/"+c.ws+"/agent", crushproto.RunRequest{SessionID: s.ID, RunID: "run-1", Prompt: prompt}, nil); code != http.StatusAccepted {
		c.t.Fatalf("start run = %d", code)
	}
	return s.ID
}

// transcript is what the TUI would draw for the session after re-reading it.
func (c *tuiClient) transcript(sid string) string {
	c.t.Helper()
	resp, err := http.Get(c.base + "/v1/workspaces/" + c.ws + "/sessions/" + sid + "/messages")
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return buf.String()
}

func handoffServer(t *testing.T, g *resumeRig, approvalID string) *httptest.Server {
	t.Helper()
	srv, err := crushproto.New(crushproto.Options{
		Runner: handoffRunner{r: newCrushRunner(g.s, ""), approvalID: approvalID},
		Logf:   func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { srv.Close(); ts.Close() })
	return ts
}

// TUI starts the run, the desktop UI approves it, and the TUI — reconnected in
// between — closes its dialog, shows the resumed output, and the tool ran once.
func TestDesktopApprovalContinuesATUIRunAcrossAReconnect(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	g.script = func(context.Context, int, pendingRun) (*agent.RunResult, error) {
		return &agent.RunResult{Content: "edited a.go and the tests pass", UsedTools: true}, nil
	}
	ts := handoffServer(t, g, id)
	c := newTUIClient(t, ts.URL)
	disconnect := c.connect()
	sid := c.startRun("fix a.go")
	c.waitFrame(`"permission_request"`, "code_edit")

	// The TUI loses its connection while the question is open, then comes back.
	disconnect()
	c.connect()
	c.waitFrame(`"permission_request"`, "code_edit") // dialog is restored

	// The desktop approves.
	out := g.s.ResolveApproval(id, true, "")
	if out["success"] != true || out["resume_error"] != nil {
		t.Fatalf("desktop resolve: %+v", out)
	}

	c.waitFrame(`"permission_notification"`, `"granted":true`) // TUI dialog closes
	c.waitFrame(`"run_complete"`)
	tr := c.transcript(sid)
	if !strings.Contains(tr, "edited a.go and the tests pass") {
		t.Fatalf("TUI transcript lacks the resumed output:\n%s", tr)
	}
	if !strings.Contains(tr, "desktop_user") {
		t.Fatalf("TUI should say who decided:\n%s", tr)
	}
	if n := g.calls.Load(); n != 1 {
		t.Fatalf("the tool call was resumed %d times, want exactly 1", n)
	}
}

// If the desktop rejects, the TUI is told and nothing runs.
func TestDesktopRejectionEndsTheTUIRunWithoutExecuting(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	ts := handoffServer(t, g, id)
	c := newTUIClient(t, ts.URL)
	c.connect()
	sid := c.startRun("fix a.go")
	c.waitFrame(`"permission_request"`)

	g.s.ResolveApproval(id, false, "no")

	c.waitFrame(`"permission_notification"`, `"denied":true`)
	c.waitFrame(`"run_complete"`)
	if !strings.Contains(c.transcript(sid), "denied") {
		t.Fatalf("TUI should show the denial:\n%s", c.transcript(sid))
	}
	if n := g.calls.Load(); n != 0 {
		t.Fatalf("a rejected tool call was resumed %d times", n)
	}
}

// Both surfaces answer at the same moment: one decision stands, the run is
// resumed once, and the TUI run still ends cleanly with that outcome.
func TestSimultaneousDecisionsResumeOnce(t *testing.T) {
	for i := 0; i < 6; i++ {
		g := newResumeRig(t)
		id := g.pause(t, "code_edit")
		g.startRun(t, "run_a")
		g.script = func(context.Context, int, pendingRun) (*agent.RunResult, error) {
			return &agent.RunResult{Content: "done", UsedTools: true}, nil
		}
		ts := handoffServer(t, g, id)
		c := newTUIClient(t, ts.URL)
		c.connect()
		c.startRun("fix a.go")
		f := c.waitFrame(`"permission_request"`)
		var env struct {
			Payload struct {
				Payload crushproto.PermissionRequest `json:"payload"`
			} `json:"payload"`
		}
		_ = json.Unmarshal([]byte(f), &env)
		perm := env.Payload.Payload

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); g.s.ResolveApproval(id, true, "") }()
		go func() {
			defer wg.Done()
			c.post("/v1/workspaces/"+c.ws+"/permissions/grant", map[string]any{"action": "allow", "permission": perm}, nil)
		}()
		wg.Wait()

		c.waitFrame(`"run_complete"`)
		if n := g.calls.Load(); n != 1 {
			t.Fatalf("round %d: resumed %d times, want exactly 1", i, n)
		}
	}
}

func TestApprovalRelayDeliversEarlyAndLateDecisions(t *testing.T) {
	var r approvalRelay

	// Decided before anyone watches: the watcher still sees it.
	r.markDecided("a", true, "desktop_user")
	w := r.watch("a")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, stop := w.Decision(ctx)
	defer stop()
	select {
	case v := <-ch:
		if !v {
			t.Fatal("decision value lost")
		}
	case <-ctx.Done():
		t.Fatal("early decision not delivered")
	}

	// Not decided yet: nothing is delivered, and stop ends the waiter.
	w2 := r.watch("b")
	ch2, stop2 := w2.Decision(context.Background())
	select {
	case <-ch2:
		t.Fatal("undecided approval reported a decision")
	case <-time.After(50 * time.Millisecond):
	}
	stop2()
	if w2.Decided(0) {
		t.Fatal("undecided approval reported as decided")
	}

	// Outcome waits for completion and honours cancellation.
	short, c2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer c2()
	if _, _, ok := w2.Outcome(short); ok {
		t.Fatal("outcome returned before completion")
	}
	r.markDecided("b", false, "x")
	r.markCompleted("b", map[string]any{"success": true})
	if ap, out, ok := w2.Outcome(context.Background()); !ok || ap || out["success"] != true {
		t.Fatalf("outcome = %v %v %v", ap, out, ok)
	}
}
