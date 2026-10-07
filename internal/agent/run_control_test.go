package agent

import (
	"context"
	"testing"
)

func TestRunControl_SteerInbox(t *testing.T) {
	rc := NewRunControl()
	sid := "test-session-123"

	// 1. Not running: AddSteer should return false
	if rc.IsRunning(sid) {
		t.Fatal("session should not be running initially")
	}
	if rc.AddSteer(sid, "steer 1") {
		t.Fatal("AddSteer should fail when session is not running")
	}

	// 2. Start running via context cancel
	ctx, cancel := context.WithCancel(context.Background())
	_ = ctx
	defer cancel()
	rc.SetCtxCancel(sid, cancel)

	if !rc.IsRunning(sid) {
		t.Fatal("session should be reported as running")
	}

	// 3. Queue steers
	if !rc.AddSteer(sid, "focus on handlers.go") {
		t.Fatal("AddSteer should succeed when session is running")
	}
	if !rc.AddSteer(sid, "also add error handling") {
		t.Fatal("second AddSteer should succeed")
	}

	// 4. Drain steers
	steers := rc.DrainSteers(sid)
	if len(steers) != 2 {
		t.Fatalf("expected 2 steers, got %d", len(steers))
	}
	if steers[0] != "focus on handlers.go" || steers[1] != "also add error handling" {
		t.Fatalf("unexpected steer content: %+v", steers)
	}

	// 5. Subsequent drain should be empty
	if drainedAgain := rc.DrainSteers(sid); len(drainedAgain) != 0 {
		t.Fatalf("subsequent drain should be empty, got %+v", drainedAgain)
	}

	// 6. Clear session
	rc.Clear(sid)
	if rc.IsRunning(sid) {
		t.Fatal("session should not be running after clear")
	}
}
