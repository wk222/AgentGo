package crushengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCrush is a scripted stand-in for `crush server` (/v1 subset).
type fakeCrush struct {
	t   *testing.T
	srv *httptest.Server

	mu              sync.Mutex
	subs            []chan string
	sessionsCreated int
	workspaces      int
	grants          []string // permission actions received
	cancels         int
	questionCancels int
	lastPrompt      string
	lastRunID       string
	onAgent         func(f *fakeCrush, runID, sid string)
	agentDelay      time.Duration // hold the 202 back (cancel-while-sending)
}

func newFakeCrush(t *testing.T) *fakeCrush {
	f := &fakeCrush{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("POST /v1/workspaces", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.workspaces++
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "ws1", "path": "x"})
	})
	mux.HandleFunc("DELETE /v1/workspaces/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("POST /v1/workspaces/{id}/sessions", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.sessionsCreated++
		n := f.sessionsCreated
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "sess" + string(rune('0'+n))})
	})
	mux.HandleFunc("GET /v1/workspaces/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		ch := make(chan string, 64)
		f.mu.Lock()
		f.subs = append(f.subs, ch)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case s, ok := <-ch:
				if !ok {
					return // simulate server closing the stream
				}
				_, _ = w.Write([]byte("data: " + s + "\n\n"))
				w.(http.Flusher).Flush()
			}
		}
	})
	mux.HandleFunc("POST /v1/workspaces/{id}/agent", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		runID, _ := body["run_id"].(string)
		sid, _ := body["session_id"].(string)
		f.mu.Lock()
		f.lastPrompt, _ = body["prompt"].(string)
		f.lastRunID = runID
		cb, delay := f.onAgent, f.agentDelay
		f.mu.Unlock()
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(http.StatusAccepted)
		if cb != nil {
			go cb(f, runID, sid)
		}
	})
	mux.HandleFunc("POST /v1/workspaces/{id}/agent/sessions/{sid}/cancel", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.cancels++
		f.mu.Unlock()
		w.WriteHeader(200)
	})
	mux.HandleFunc("POST /v1/workspaces/{id}/permissions/grant", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.grants = append(f.grants, body.Action)
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"resolved":true}`))
	})
	mux.HandleFunc("POST /v1/workspaces/{id}/questions/cancel", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		f.questionCancels++
		f.mu.Unlock()
		_, _ = w.Write([]byte(`{"resolved":true}`))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCrush) addr() string { return strings.TrimPrefix(f.srv.URL, "http://") }

// push emits one wrapped SSE record: {"type":T,"payload":{"type":"updated","payload":P}}.
func (f *fakeCrush) push(typ string, payload any) {
	b, _ := json.Marshal(map[string]any{
		"type": typ, "payload": map[string]any{"type": "updated", "payload": payload},
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs {
		ch <- string(b)
	}
}

func (f *fakeCrush) closeStreams() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs {
		close(ch)
	}
	f.subs = nil
}

func (f *fakeCrush) snapshot() (grants []string, cancels, qcancels, sessions int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.grants...), f.cancels, f.questionCancels, f.sessionsCreated
}

func msg(id, role, sid string, parts ...wirePart) map[string]any {
	return map[string]any{"id": id, "role": role, "session_id": sid, "parts": parts}
}

func newTestEngine(f *fakeCrush, mut func(*Config)) *Engine {
	cfg := Config{ServerAddr: f.addr(), DefaultWorkspace: "."}
	if mut != nil {
		mut(&cfg)
	}
	return New(cfg)
}

func runEngine(t *testing.T, e *Engine, ctx context.Context, input, session string) (*recEm, resultT) {
	t.Helper()
	em := &recEm{}
	done := make(chan resultT, 1)
	go func() {
		res, err := e.Run(ctx, runReq(input, session), em)
		done <- resultT{text: firstText(res.Messages), errStr: res.Error, err: err}
	}()
	select {
	case r := <-done:
		return em, r
	case <-time.After(10 * time.Second):
		t.Fatal("engine.Run timed out")
		return nil, resultT{}
	}
}

func TestRunHappyPath(t *testing.T) {
	f := newFakeCrush(t)
	f.onAgent = func(f *fakeCrush, runID, sid string) {
		f.push("message", msg("m1", "assistant", sid, part("text", textData{Text: "Fix"})))
		f.push("message", msg("m1", "assistant", sid,
			part("text", textData{Text: "Fixing"}),
			part("tool_call", toolCallData{ID: "c1", Name: "edit", Input: `{"p":1}`, Finished: true})))
		f.push("permission_request", wirePermission{ID: "p1", SessionID: sid, ToolName: "edit", Description: "edit a.go"})
		f.push("file", wireFile{SessionID: sid, Path: "a.go", Content: "old", Version: 0})
		f.push("file", wireFile{SessionID: sid, Path: "a.go", Content: "new", Version: 1})
		f.push("message", msg("m2", "tool", sid, part("tool_result", toolResultData{ToolCallID: "c1", Name: "edit", Content: "done"})))
		// a different run's completion must be ignored
		f.push("run_complete", wireRunComplete{SessionID: sid, RunID: "someone-else", Text: "WRONG"})
		f.push("run_complete", wireRunComplete{SessionID: sid, RunID: runID, Text: "All fixed."})
	}
	e := newTestEngine(f, func(c *Config) { c.AutoApprove = true })
	em, r := runEngine(t, e, context.Background(), "fix it", "agentgo-s1")
	if r.err != nil || r.errStr != "" || r.text != "All fixed." {
		t.Fatalf("result = %+v", r)
	}
	expect(t, em.got,
		"status:crush_started",
		"tok:Fix", "tok:ing",
		`call:c1:edit:{"p":1}`,
		"approval:p1:edit",
		"file:a.go:old->new",
		"result:c1:done",
	)
	waitFor(t, func() bool { g, _, _, _ := f.snapshot(); return len(g) == 1 })
	if g, _, _, _ := f.snapshot(); g[0] != permAllow {
		t.Fatalf("grant = %v, want allow", g)
	}
	if f.lastPrompt != "fix it" {
		t.Fatalf("prompt = %q", f.lastPrompt)
	}
}

func TestSessionMappingIsReusedAndWorkspaceCreatedOnce(t *testing.T) {
	f := newFakeCrush(t)
	f.onAgent = func(f *fakeCrush, runID, sid string) {
		f.push("run_complete", wireRunComplete{SessionID: sid, RunID: runID, Text: "ok"})
	}
	e := newTestEngine(f, nil)
	runEngine(t, e, context.Background(), "a", "same")
	runEngine(t, e, context.Background(), "b", "same")
	runEngine(t, e, context.Background(), "c", "") // anonymous -> new session each time
	runEngine(t, e, context.Background(), "d", "")
	if _, _, _, n := f.snapshot(); n != 3 { // 1 shared + 2 anonymous
		t.Fatalf("sessions created = %d, want 3", n)
	}
	if f.workspaces != 1 {
		t.Fatalf("workspaces created = %d, want 1", f.workspaces)
	}
}

func TestApproverCanDeny(t *testing.T) {
	f := newFakeCrush(t)
	f.onAgent = func(f *fakeCrush, runID, sid string) {
		f.push("permission_request", wirePermission{ID: "p1", SessionID: sid, ToolName: "bash"})
		f.push("permission_request", wirePermission{ID: "p1", SessionID: sid, ToolName: "bash"}) // duplicate id
		time.Sleep(100 * time.Millisecond)
		f.push("run_complete", wireRunComplete{SessionID: sid, RunID: runID, Text: "ok"})
	}
	var asked []string
	var mu sync.Mutex
	e := newTestEngine(f, func(c *Config) {
		c.AutoApprove = true // must be overridden by Approver
		c.Approver = func(_ context.Context, p PermissionRequest) bool {
			mu.Lock()
			asked = append(asked, p.ToolName)
			mu.Unlock()
			return false
		}
	})
	runEngine(t, e, context.Background(), "x", "s")
	waitFor(t, func() bool { g, _, _, _ := f.snapshot(); return len(g) >= 1 })
	g, _, _, _ := f.snapshot()
	mu.Lock()
	defer mu.Unlock()
	if len(g) != 1 || g[0] != permDeny || len(asked) != 1 || asked[0] != "bash" {
		t.Fatalf("grants=%v asked=%v", g, asked)
	}
}

func TestCancelPropagatesToCrush(t *testing.T) {
	f := newFakeCrush(t)
	started := make(chan struct{})
	f.onAgent = func(f *fakeCrush, runID, sid string) {
		close(started)
		// Crush reacts to the cancel request with a cancelled run_complete.
		waitFor(t, func() bool { _, c, _, _ := f.snapshot(); return c == 1 })
		f.push("run_complete", wireRunComplete{SessionID: sid, RunID: runID, Cancelled: true})
	}
	e := newTestEngine(f, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; time.Sleep(100 * time.Millisecond); cancel() }()
	_, r := runEngine(t, e, ctx, "long task", "s")
	if r.err != nil || r.errStr != "cancelled" {
		t.Fatalf("result = %+v err=%v", r, r.err)
	}
	if _, c, _, _ := f.snapshot(); c != 1 {
		t.Fatalf("cancel calls = %d", c)
	}
}

func TestCancelWhilePromptIsInFlightIsACleanCancel(t *testing.T) {
	f := newFakeCrush(t)
	f.agentDelay = 300 * time.Millisecond // 202 arrives after the user cancels
	e := newTestEngine(f, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, r := runEngine(t, e, ctx, "x", "s")
	if r.err != nil || r.errStr != "cancelled" {
		t.Fatalf("result = %+v err=%v", r, r.err)
	}
	waitFor(t, func() bool { _, c, _, _ := f.snapshot(); return c == 1 }) // best-effort cancel sent
}

func TestStreamClosedBeforeCompletionIsAnError(t *testing.T) {
	f := newFakeCrush(t)
	f.onAgent = func(f *fakeCrush, runID, sid string) { f.closeStreams() }
	e := newTestEngine(f, nil)
	_, r := runEngine(t, e, context.Background(), "x", "s")
	if r.err == nil || !strings.Contains(r.err.Error(), "stream") {
		t.Fatalf("result = %+v", r)
	}
}

func TestRunCompleteErrorIsSurfaced(t *testing.T) {
	f := newFakeCrush(t)
	f.onAgent = func(f *fakeCrush, runID, sid string) {
		f.push("run_complete", wireRunComplete{SessionID: sid, RunID: runID, Error: "model exploded"})
	}
	_, r := runEngine(t, newTestEngine(f, nil), context.Background(), "x", "s")
	if r.err != nil || r.errStr != "model exploded" {
		t.Fatalf("result = %+v", r)
	}
}

func TestQuestionIsDismissed(t *testing.T) {
	f := newFakeCrush(t)
	f.onAgent = func(f *fakeCrush, runID, sid string) {
		f.push("question_batch_request", wireQuestion{ID: "q1", SessionID: sid})
		waitFor(t, func() bool { _, _, q, _ := f.snapshot(); return q == 1 })
		f.push("run_complete", wireRunComplete{SessionID: sid, RunID: runID, Text: "ok"})
	}
	em, r := runEngine(t, newTestEngine(f, nil), context.Background(), "x", "s")
	if r.err != nil || r.errStr != "" {
		t.Fatalf("result = %+v", r)
	}
	expect(t, em.got, "status:crush_started", "status:question_dismissed")
}

func TestValidation(t *testing.T) {
	f := newFakeCrush(t)
	e := New(Config{ServerAddr: f.addr()}) // no workspace
	if _, err := e.Run(context.Background(), runReq("x", "s"), &recEm{}); err == nil {
		t.Fatal("expected workspace error")
	}
	e = newTestEngine(f, nil)
	if _, err := e.Run(context.Background(), runReq("  ", "s"), &recEm{}); err == nil {
		t.Fatal("expected empty-input error")
	}
}
