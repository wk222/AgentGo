package bridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/checkpoint"
	"agentgo/internal/governance"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

type stubExecuteBash struct{}

func (stubExecuteBash) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "execute_bash",
		Desc: "stub for workflow HITL test",
	}, nil
}

func (stubExecuteBash) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return `{"stdout":"wf_ok","stderr":"","exit_code":0}`, nil
}

func TestWorkflowToolApprovalResolveResumesRun(t *testing.T) {
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
	queue, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	reg := tools.NewRegistry()
	reg.AddTool(stubExecuteBash{})

	def := workflow.Definition{
		ID:   "wf_tool_approval",
		Name: "tool approval hitl",
		Nodes: []workflow.Node{
			{ID: "n1", Type: "input"},
			{ID: "n2", Type: "tool", ToolName: "execute_bash", ArgsJSON: `{"command":"echo wf_ok"}`},
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
		wfStore:   wfStore,
		cpStore:   cpStore,
		approvals: queue,
		toolReg:   reg,
		pending:   newPendingStore(),
	}
	rt.wfExec = rt.executeWorkflow
	rt.wfResume = rt.resumeWorkflow

	ctx := context.Background()
	_, err = rt.executeWorkflow(ctx, def.ID, "hello")
	ie, ok := workflow.AsInterrupt(err)
	if !ok {
		t.Fatalf("expected interrupt, got %v", err)
	}
	meta := workflow.MetaFromInterrupt(ie)
	if meta.Kind != workflow.InterruptToolApproval {
		t.Fatalf("kind=%s meta=%+v", meta.Kind, meta)
	}
	if meta.ApprovalID == "" {
		t.Fatal("missing approval id")
	}

	rt.workflowInterruptResponse(def.ID, ie)

	pr, ok := rt.pending.Get(meta.ApprovalID)
	if !ok || pr.ResumeKind != "workflow" {
		t.Fatalf("pending=%+v ok=%v", pr, ok)
	}

	resume := &governance.ResumePayload{Approved: true}
	if err := queue.Resolve(ctx, meta.ApprovalID, true, "test", "tester", resume); err != nil {
		t.Fatal(err)
	}
	out, err := rt.resumeWorkflowAfterApproval(ctx, pr, resume)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("empty output")
	}
	// Tool output is JSON from execute_bash.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err == nil {
		if stdout, _ := parsed["stdout"].(string); stdout != "" && !strings.Contains(stdout, "wf_ok") {
			t.Fatalf("stdout=%q out=%s", stdout, out)
		}
	} else if !strings.Contains(out, "wf_ok") {
		t.Fatalf("output=%q", out)
	}
}
