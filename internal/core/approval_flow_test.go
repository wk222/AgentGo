package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentgo/internal/engine"
	"agentgo/internal/governance"
)

type fakeResolver struct {
	err   error
	calls int
	last  *governance.ResumePayload
}

func (f *fakeResolver) Resolve(_ context.Context, _ string, _ bool, _, _ string, r *governance.ResumePayload) error {
	f.calls++
	f.last = r
	return f.err
}

func newFlow(r ApprovalResolver) *ApprovalFlow {
	return &ApprovalFlow{Approvals: r, Pending: NewPendingStore(), Relay: &ApprovalRelay{}}
}

func TestDecideClaimsPendingRunOnce(t *testing.T) {
	r := &fakeResolver{}
	f := newFlow(r)
	f.Pending.Set(PendingRun{ApprovalID: "a1", SessionID: "s", ToolName: "execute_bash", Arguments: `{"c":1}`})

	d, out, complete, ok := f.Decide(context.Background(), "a1", true, "", "me", `{"c":2}`)
	if !ok || !d.Found || out["success"] != true || complete == nil {
		t.Fatalf("decide: ok=%v found=%v out=%v", ok, d.Found, out)
	}
	if d.Run.Arguments != `{"c":2}` || !d.Resume.Approved || d.Resume.Arguments != `{"c":2}` {
		t.Fatalf("final args not applied: %+v %+v", d.Run, d.Resume)
	}
	complete()

	// The run was claimed: a second decision finds nothing to resume.
	d2, out2, _, ok2 := f.Decide(context.Background(), "a1", true, "", "me", "")
	if !ok2 || d2.Found || out2["resume_error"] != ErrNoPendingRun.Error() {
		t.Fatalf("second decide: ok=%v found=%v out=%v", ok2, d2.Found, out2)
	}
}

func TestDecideRejectWithNothingPendingIsNotAnError(t *testing.T) {
	f := newFlow(&fakeResolver{})
	d, out, _, ok := f.Decide(context.Background(), "gone", false, "", "me", "")
	if !ok || d.Found {
		t.Fatalf("ok=%v found=%v", ok, d.Found)
	}
	if _, has := out["resume_error"]; has {
		t.Fatalf("a refusal must not report resume_error: %v", out)
	}
}

func TestDecideStopsWhenTheDecisionCannotBeRecorded(t *testing.T) {
	r := &fakeResolver{err: errors.New("already resolved")}
	f := newFlow(r)
	f.Pending.Set(PendingRun{ApprovalID: "a1"})
	_, out, complete, ok := f.Decide(context.Background(), "a1", true, "", "me", "")
	if ok || complete != nil || out["success"] != false || !strings.Contains(out["error"].(string), "already resolved") {
		t.Fatalf("ok=%v out=%v", ok, out)
	}
	if _, still := f.Pending.Get("a1"); !still {
		t.Fatal("a failed decision must leave the pending run in place")
	}
}

func TestDecideHonoursCancelledContext(t *testing.T) {
	r := &fakeResolver{}
	f := newFlow(r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, out, _, ok := f.Decide(ctx, "a1", true, "", "me", "")
	if ok || out["success"] != false || r.calls != 0 {
		t.Fatalf("ok=%v out=%v calls=%d", ok, out, r.calls)
	}
}

func TestRunLogCutStringNeverSplitsARune(t *testing.T) {
	s := strings.Repeat("中", 10)
	for max := 1; max < len(s); max++ {
		got := strings.TrimSuffix(CutString(s, max), RunLogCutMark)
		if !strings.HasPrefix(s, got) || len([]rune(got))*3 != len(got) {
			t.Fatalf("max=%d cut a rune: %q", max, got)
		}
	}
}

func TestDurableRunEventSkipsTransientAndDone(t *testing.T) {
	if _, ok := DurableRunEvent(engine.Event{Type: engine.EventDone, RunID: "r", SessionID: "s"}); ok {
		t.Fatal("Done is written explicitly, not mapped")
	}
	ev, ok := DurableRunEvent(engine.Event{Type: engine.EventToolCall, RunID: "r", SessionID: "s", Payload: map[string]any{"id": "c", "name": "n", "arguments": "{}"}})
	if !ok || ev.CallID != "c" || ev.Name != "n" {
		t.Fatalf("tool call not mapped: %+v ok=%v", ev, ok)
	}
	if _, ok := DurableRunEvent(engine.Event{Type: engine.EventToolCall, Payload: map[string]any{"id": "c"}}); ok {
		t.Fatal("events without run/session ids are not stored")
	}
}
