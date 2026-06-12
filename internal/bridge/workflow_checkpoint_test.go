package bridge

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"

	"agentgo/internal/checkpoint"
	"agentgo/internal/workflow"
)

func TestRuntimeWorkflowWaitSignalCheckpointResume(t *testing.T) {
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

	def := workflow.Definition{
		ID:   "wf_bridge_wait_cp",
		Name: "bridge wait checkpoint",
		Nodes: []workflow.Node{
			{ID: "n1", Type: "input"},
			{ID: "n2", Type: "wait_signal", Config: map[string]any{"signal": "go"}},
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

	rt := &Runtime{wfStore: wfStore, cpStore: cpStore}
	ctx := context.Background()

	_, err = rt.executeWorkflow(ctx, def.ID, "hello")
	ie, ok := workflow.AsInterrupt(err)
	if !ok {
		t.Fatalf("expected interrupt, got err=%v", err)
	}
	if ie.CheckPointID == "" || ie.InterruptID == "" {
		t.Fatalf("missing ids: %+v", ie)
	}

	out, err := rt.resumeWorkflow(ctx, def.ID, ie.CheckPointID, ie.InterruptID, `{"payload":"resumed"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "resumed" {
		t.Fatalf("output=%q want resumed", out)
	}
}

func TestWorkflowCheckpointDisabled(t *testing.T) {
	prev := os.Getenv("AGENTGO_WORKFLOW_CHECKPOINT")
	t.Setenv("AGENTGO_WORKFLOW_CHECKPOINT", "0")
	t.Cleanup(func() { _ = os.Setenv("AGENTGO_WORKFLOW_CHECKPOINT", prev) })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	cpStore, _ := checkpoint.OpenSQLiteStore(db)
	rt := &Runtime{cpStore: cpStore}
	rc := rt.workflowRunContextPtr("x")
	if rc.CheckPointStore != nil {
		t.Fatal("checkpoint store should be nil when disabled")
	}
}
