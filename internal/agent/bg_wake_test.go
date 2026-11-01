package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

type wakeProbe struct {
	mu    sync.Mutex
	calls []string
	ch    chan struct{}
}

func newWakeProbe() *wakeProbe { return &wakeProbe{ch: make(chan struct{}, 8)} }

func (p *wakeProbe) handle(session, text string) {
	p.mu.Lock()
	p.calls = append(p.calls, session+"|"+text)
	p.mu.Unlock()
	p.ch <- struct{}{}
}

func (p *wakeProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func fastWake(idle func(string) bool, p *wakeProbe) *bgWake {
	w := newBGWake(idle)
	w.debounce = 20 * time.Millisecond
	w.maxWait = 2 * time.Second
	w.setHandler(p.handle)
	return w
}

func waitWake(t *testing.T, p *wakeProbe) {
	t.Helper()
	select {
	case <-p.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("no wake arrived")
	}
}

func TestFinishedTaskWakesItsIdleSession(t *testing.T) {
	p := newWakeProbe()
	w := fastWake(func(string) bool { return true }, p)
	w.done(BackgroundDone{SessionID: "s1", Kind: "bash", ID: "t1", Title: "make", Status: "completed", Output: "built ok"})
	waitWake(t, p)
	if !strings.HasPrefix(p.calls[0], "s1|") || !strings.Contains(p.calls[0], "built ok") || !strings.Contains(p.calls[0], "t1") {
		t.Fatalf("wake message missing the result: %q", p.calls[0])
	}
}

func TestTasksFinishingTogetherWakeOnce(t *testing.T) {
	p := newWakeProbe()
	w := fastWake(func(string) bool { return true }, p)
	for _, id := range []string{"a", "b", "c"} {
		w.done(BackgroundDone{SessionID: "s1", Kind: "bash", ID: id, Title: id, Status: "completed"})
	}
	waitWake(t, p)
	time.Sleep(100 * time.Millisecond)
	if p.count() != 1 {
		t.Fatalf("three results should make one turn, got %d", p.count())
	}
	for _, id := range []string{"a", "b", "c"} {
		if !strings.Contains(p.calls[0], id) {
			t.Fatalf("task %s missing from %q", id, p.calls[0])
		}
	}
}

func TestResultTheAgentAlreadyReadIsNotAnnounced(t *testing.T) {
	p := newWakeProbe()
	busy := true
	var mu sync.Mutex
	w := fastWake(func(string) bool { mu.Lock(); defer mu.Unlock(); return !busy }, p)
	w.done(BackgroundDone{SessionID: "s1", Kind: "bash", ID: "read", Title: "x", Status: "completed"})
	w.done(BackgroundDone{SessionID: "s1", Kind: "bash", ID: "unread", Title: "y", Status: "completed"})
	w.collect("read") // the turn is still running and collects one with task_output
	mu.Lock()
	busy = false
	mu.Unlock()
	waitWake(t, p)
	if strings.Contains(p.calls[0], "bash read（") || !strings.Contains(p.calls[0], "bash unread（") {
		t.Fatalf("only the unread result belongs in the wake: %q", p.calls[0])
	}
}

func TestReadBeforeItWasNotedStillCounts(t *testing.T) {
	p := newWakeProbe()
	w := fastWake(func(string) bool { return true }, p)
	w.collect("t9") // task_output returned before the done callback ran
	w.done(BackgroundDone{SessionID: "s1", Kind: "bash", ID: "t9", Title: "x", Status: "completed"})
	time.Sleep(150 * time.Millisecond)
	if p.count() != 0 {
		t.Fatalf("an already-read result woke the session: %v", p.calls)
	}
}

func TestBusySessionIsNotInterrupted(t *testing.T) {
	p := newWakeProbe()
	var mu sync.Mutex
	busy := true
	w := fastWake(func(string) bool { mu.Lock(); defer mu.Unlock(); return !busy }, p)
	w.done(BackgroundDone{SessionID: "s1", Kind: "job", ID: "j", Title: "train", Status: "completed"})
	time.Sleep(150 * time.Millisecond)
	if p.count() != 0 {
		t.Fatal("woke a session that was mid-turn")
	}
	mu.Lock()
	busy = false
	mu.Unlock()
	waitWake(t, p)
}

func TestStoppedTaskAndSessionlessTaskWakeNobody(t *testing.T) {
	p := newWakeProbe()
	w := fastWake(func(string) bool { return true }, p)
	w.done(BackgroundDone{SessionID: "s1", Kind: "bash", ID: "c", Status: "canceled"})
	w.done(BackgroundDone{SessionID: "", Kind: "bash", ID: "n", Status: "completed"})
	time.Sleep(150 * time.Millisecond)
	if p.count() != 0 {
		t.Fatalf("unexpected wake: %v", p.calls)
	}
}

func TestBashResultSummary(t *testing.T) {
	st, code, out := bashResultSummary(`{"stdout":"hi","stderr":"warn","exit_code":0}`, nil)
	if st != "completed" || code != 0 || !strings.Contains(out, "hi") || !strings.Contains(out, "warn") {
		t.Fatalf("got %s %d %q", st, code, out)
	}
	if st, code, _ := bashResultSummary(`{"stdout":"","stderr":"boom","exit_code":2}`, nil); st != "failed" || code != 2 {
		t.Fatalf("non-zero exit should be failed: %s %d", st, code)
	}
	if st, _, _ := bashResultSummary("", context.Canceled); st != "canceled" {
		t.Fatalf("a stopped task is canceled, got %s", st)
	}
	if st, _, _ := bashResultSummary("", errors.New("exec: not found")); st != "failed" {
		t.Fatalf("an error is failed, got %s", st)
	}
}

func TestTaskOutputCallMarksItRead(t *testing.T) {
	p := newWakeProbe()
	w := fastWake(func(string) bool { return true }, p)
	mw := newBGCollectMiddleware(w).(*bgCollectMiddleware)
	w.done(BackgroundDone{SessionID: "s1", Kind: "bash", ID: "task-1", Title: "x", Status: "completed"})
	// Still running: nothing is read yet.
	mw.observe(`{"task_id":"task-1","block":false}`, "Task ID: task-1\nStatus: running")
	// Final status: read.
	mw.observe(`{"task_id":"task-1","block":true}`, "Task ID: task-1\nStatus: completed\nResult: ok")
	time.Sleep(150 * time.Millisecond)
	if p.count() != 0 {
		t.Fatalf("a result the agent fetched was announced again: %v", p.calls)
	}
}

func TestCollectMiddlewareOnlyWrapsTaskOutput(t *testing.T) {
	mw := newBGCollectMiddleware(newBGWake(nil))
	called := false
	ep := adk.InvokableToolCallEndpoint(func(context.Context, string, ...tool.Option) (string, error) { called = true; return "x", nil })
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), ep, &adk.ToolContext{Name: "execute_bash"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped(context.Background(), "{}"); err != nil || !called {
		t.Fatal("other tools must pass straight through")
	}
}
