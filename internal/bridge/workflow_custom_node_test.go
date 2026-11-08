package bridge

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/checkpoint"
	"agentgo/internal/core"
	"agentgo/internal/governance"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

// recordingBash stands in for execute_bash (always held for approval) and remembers its arguments.
type recordingBash struct{ args *[]string }

func (recordingBash) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "execute_bash", Desc: "stub for custom node approval test"}, nil
}

func (r recordingBash) InvokableRun(_ context.Context, args string, _ ...tool.Option) (string, error) {
	*r.args = append(*r.args, args)
	return `{"stdout":"wf_ok","stderr":"","exit_code":0}`, nil
}

// A custom node is governed like any other caller of its backend: when the
// backend needs approval the run pauses, and after approval the node runs with the
// inputs it had bound before the pause.
func TestCustomNodeBackendApprovalPausesAndResumesTheRun(t *testing.T) {
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

	var seen []string
	reg := tools.NewRegistry()
	reg.AddTool(recordingBash{args: &seen})

	if _, err := wfStore.SaveNode(workflow.NodeSpec{
		ID: "shell_echo", Description: "echoes a word through the shell",
		Inputs:  []workflow.NodeField{{Name: "word"}},
		Backend: workflow.NodeBackend{Kind: "tool", Ref: "execute_bash", Args: map[string]any{"command": "echo {{in.word}}"}},
	}); err != nil {
		t.Fatal(err)
	}
	def := workflow.Definition{
		ID: "wf_custom_approval", Name: "custom node approval",
		Nodes: []workflow.Node{
			{ID: "s", Type: "start"},
			{ID: "n", Type: "custom:shell_echo", Config: map[string]any{"inputs": map[string]any{"word": "{{input}}"}}},
			{ID: "e", Type: "end"},
		},
		Edges: []workflow.Edge{{From: "s", To: "n"}, {From: "n", To: "e"}},
	}
	if err := wfStore.Save(def); err != nil {
		t.Fatal(err)
	}

	rt := &Runtime{wfStore: wfStore, cpStore: cpStore, approvals: queue, toolReg: reg, pending: core.NewPendingStore()}
	rt.wfExec = rt.executeWorkflow
	rt.wfResume = rt.resumeWorkflow

	ctx := context.Background()
	_, err = rt.executeWorkflow(ctx, def.ID, "hello")
	ie, ok := workflow.AsInterrupt(err)
	if !ok {
		t.Fatalf("a backend that needs approval must pause the run, got %v", err)
	}
	meta := workflow.MetaFromInterrupt(ie)
	if meta.Kind != workflow.InterruptToolApproval || meta.ApprovalID == "" {
		t.Fatalf("meta = %+v", meta)
	}
	if len(seen) != 0 {
		t.Fatalf("nothing may run before approval: %v", seen)
	}

	rt.workflowInterruptResponse(def.ID, ie)
	pr, ok := rt.pending.Get(meta.ApprovalID)
	if !ok || pr.ResumeKind != "workflow" {
		t.Fatalf("pending = %+v ok=%v", pr, ok)
	}
	resume := &governance.ResumePayload{Approved: true}
	if err := queue.Resolve(ctx, meta.ApprovalID, true, "test", "tester", resume); err != nil {
		t.Fatal(err)
	}
	out, err := rt.resumeWorkflowAfterApproval(ctx, pr, resume)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "wf_ok") {
		t.Fatalf("output = %q", out)
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "echo hello") {
		t.Fatalf("the node must run once, with the input it bound before the pause: %v", seen)
	}
}
