package agent

import (
	"testing"
	"time"
)

func TestRunControlRunningListsSessionsOldestFirst(t *testing.T) {
	rc := NewRunControl()
	if got := rc.Running(); len(got) != 0 {
		t.Fatalf("idle control lists %+v", got)
	}
	noop := func() {}

	rc.SetCtxCancel("agentic:b", noop) // the prefix does not make it a different session
	time.Sleep(5 * time.Millisecond)
	rc.SetCtxCancel("a", noop)
	rc.SetCtxCancel("b", noop) // registering again must not restart the clock

	got := rc.Running()
	if len(got) != 2 || got[0].SessionID != "b" || got[1].SessionID != "a" {
		t.Fatalf("running = %+v, want b then a", got)
	}
	if got[0].StartedAt.IsZero() || !got[0].StartedAt.Before(got[1].StartedAt) {
		t.Fatalf("start times = %v, %v", got[0].StartedAt, got[1].StartedAt)
	}
}

func TestRunControlRunningForgetsClearedRuns(t *testing.T) {
	rc := NewRunControl()
	noop := func() {}
	rc.SetCtxCancel("a", noop)
	rc.SetCtxCancel("b", noop)
	rc.Clear("a")
	if got := rc.Running(); len(got) != 1 || got[0].SessionID != "b" {
		t.Fatalf("running = %+v", got)
	}

	// A new run of the same session starts a new clock.
	first := rc.Running()[0].StartedAt
	rc.Clear("b")
	time.Sleep(5 * time.Millisecond)
	rc.SetCtxCancel("b", noop)
	if again := rc.Running()[0].StartedAt; !again.After(first) {
		t.Fatalf("restarted run kept the old start: %v then %v", first, again)
	}
}

func TestRunControlRunningIsSafeWithoutAControl(t *testing.T) {
	var rc *RunControl
	if got := rc.Running(); got != nil {
		t.Fatalf("nil control lists %+v", got)
	}
	var r *Runner
	if got := r.RunningSessions(); got != nil {
		t.Fatalf("nil runner lists %+v", got)
	}
}
