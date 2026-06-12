package admin

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func TestAdminStateTransitions(t *testing.T) {
	// Legal transitions
	if !CanTransition(StatusPending, StatusRunning) {
		t.Fatal("pending→running should be legal")
	}
	if !CanTransition(StatusRunning, StatusPaused) {
		t.Fatal("running→paused should be legal")
	}
	if !CanTransition(StatusPaused, StatusRunning) {
		t.Fatal("paused→running should be legal")
	}
	if !CanTransition(StatusRunning, StatusCancelled) {
		t.Fatal("running→cancelled should be legal")
	}
	if !CanTransition(StatusPaused, StatusCancelled) {
		t.Fatal("paused→cancelled should be legal")
	}

	// Illegal transitions
	if CanTransition(StatusCompleted, StatusRunning) {
		t.Fatal("completed→running should be illegal")
	}
	if CanTransition(StatusCancelled, StatusRunning) {
		t.Fatal("cancelled→running should be illegal")
	}
}

type mockAgentRunner struct {
	runCount int
	output   string
	err      error
}

func (m *mockAgentRunner) RunAdminTask(ctx context.Context, sessionID, prompt string) (string, error) {
	m.runCount++
	return m.output, m.err
}

func TestAdminRunnerControlsAndTracing(t *testing.T) {
	dbFile := "test_admin_control.db"
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()

	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	agent := &mockAgentRunner{output: "Step completed successfully."}
	runner := NewAdminRunner(store, agent)

	ctx := context.Background()

	// Enqueue a task
	task, err := runner.EnqueueTask(ctx, "Test goal step by step")
	if err != nil {
		t.Fatalf("failed to enqueue: %v", err)
	}

	// Verify task exists and is pending
	if task.Status != StatusPending {
		t.Errorf("expected StatusPending, got %s", task.Status)
	}

	// Pause task
	err = runner.Pause(ctx, task.ID)
	if err != nil {
		t.Fatalf("failed to pause task: %v", err)
	}
	tPaused, _ := store.GetTask(ctx, task.ID)
	if tPaused.Status != StatusPaused {
		t.Errorf("expected StatusPaused, got %s", tPaused.Status)
	}

	// Resume task
	err = runner.Resume(ctx, task.ID)
	if err != nil {
		t.Fatalf("failed to resume task: %v", err)
	}
	tResumed, _ := store.GetTask(ctx, task.ID)
	if tResumed.Status != StatusPending {
		t.Errorf("expected StatusPending after resume, got %s", tResumed.Status)
	}

	// run one tick - should advance task
	runner.processPendingTasks(ctx)

	tRunning, _ := store.GetTask(ctx, task.ID)
	if tRunning.Status != StatusPending { // advanced steps revert to pending or completed
		t.Logf("advanced status: %s, current step: %d", tRunning.Status, tRunning.CurrentStep)
	}

	// Verify StepTrace was saved
	traces, err := store.ListStepTraces(ctx, task.ID)
	if err != nil {
		t.Fatalf("failed to list traces: %v", err)
	}
	if len(traces) == 0 {
		t.Error("expected at least one step trace saved")
	} else {
		first := traces[0]
		if first.Action != "run_step" || first.Output != "Step completed successfully." {
			t.Errorf("unexpected step trace details: %+v", first)
		}
	}

	// Takeover
	err = runner.TakeOver(ctx, task.ID, "Manual human intervention output")
	if err != nil {
		t.Fatalf("failed to takeover: %v", err)
	}

	traces, _ = store.ListStepTraces(ctx, task.ID)
	foundTakeover := false
	for _, tr := range traces {
		if tr.Action == "human_takeover" {
			foundTakeover = true
			if tr.Output != "Manual human intervention output" {
				t.Errorf("unexpected takeover output: %s", tr.Output)
			}
		}
	}
	if !foundTakeover {
		t.Error("human takeover trace not found")
	}

	// Cancel task
	err = runner.Cancel(ctx, task.ID)
	if err != nil {
		t.Fatalf("failed to cancel: %v", err)
	}
	tCancelled, _ := store.GetTask(ctx, task.ID)
	if tCancelled.Status != StatusCancelled {
		t.Errorf("expected StatusCancelled, got %s", tCancelled.Status)
	}

	// Diagnose stuck
	diag := runner.DiagnoseStuck(ctx, task.ID)
	if diag == "" {
		t.Error("diagnose stuck returned empty description")
	}
}
