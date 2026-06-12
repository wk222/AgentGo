package bridge

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"agentgo/internal/checkpoint"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

func TestWorkflowExecutionHistoryPersistence(t *testing.T) {
	t.Setenv("AGENTGO_WORKFLOW_CHECKPOINT", "1")

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	wfStore, err := workflow.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	cpStore, err := checkpoint.OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}

	reg := tools.NewRegistry()

	// Simple chain: n1 -> n2 -> n3 (using transform which is supported by executor)
	def := workflow.Definition{
		ID:   "wf_history_test",
		Name: "history test workflow",
		Nodes: []workflow.Node{
			{ID: "n1", Type: "input"},
			{ID: "n2", Type: "transform", ArgsJSON: `{"script":"return 'hello ' + input"}`},
			{ID: "n3", Type: "output"},
		},
		Edges: []workflow.Edge{
			{From: "n1", To: "n2"},
			{From: "n2", To: "n3"},
		},
	}
	if err := wfStore.Save(def); err != nil {
		t.Fatal(err)
	}

	rt := &Runtime{
		wfStore: wfStore,
		cpStore: cpStore,
		toolReg: reg,
	}

	ctx := context.Background()
	_, err = rt.executeWorkflow(ctx, def.ID, "world")
	if err != nil {
		t.Fatal(err)
	}

	// Verify the workflow execution run record was persisted
	runs, err := wfStore.ListRuns(def.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run record, got %d", len(runs))
	}

	run := runs[0]
	if run.WorkflowID != def.ID {
		t.Errorf("expected workflow_id %q, got %q", def.ID, run.WorkflowID)
	}
	if run.Input != "world" {
		t.Errorf("expected input %q, got %q", "world", run.Input)
	}
	if run.Status != "completed" {
		t.Errorf("expected status 'completed', got %q", run.Status)
	}

	// Verify step execution trace records were persisted
	steps, err := wfStore.GetSteps(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The nodes: n1, n2, n3. So we expect 3 steps.
	if len(steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(steps))
	}

	for _, step := range steps {
		if step.RunID != run.ID {
			t.Errorf("expected run_id %q, got %q", run.ID, step.RunID)
		}
		if step.Status != "completed" {
			t.Errorf("expected step %q status 'completed', got %q", step.NodeID, step.Status)
		}
	}

	// Check start node input/output
	if steps[0].NodeID != "n1" {
		t.Errorf("expected first step node_id 'n1', got %q", steps[0].NodeID)
	}

	// Check n2 node input/output/type
	if steps[1].NodeID != "n2" {
		t.Errorf("expected second step node_id 'n2', got %q", steps[1].NodeID)
	}
	if steps[1].NodeType != "transform" {
		t.Errorf("expected step node_type 'transform', got %q", steps[1].NodeType)
	}
}
