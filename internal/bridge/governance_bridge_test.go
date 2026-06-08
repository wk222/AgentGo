package bridge

import (
	"context"
	"testing"

	"agentgo/internal/governance"
)

// TestBridgeGovernancePendingResolve wires approval queue + pending store like chat HITL.
func TestBridgeGovernancePendingResolve(t *testing.T) {
	queue, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	rt := &Runtime{
		approvals: queue,
		pending:   newPendingStore(),
	}
	ctx := context.Background()

	req := governance.NewApprovalRequest(
		string(governance.KindToolApproval),
		"test",
		"Approve execute_bash",
		"high risk",
	)
	req.Fingerprint = governance.ComputePayloadHash("execute_bash", `{"command":"ls"}`)
	if err := queue.CreateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}

	rt.pending.Set(pendingRun{
		SessionID:   "sess-1",
		ToolName:    "execute_bash",
		Arguments:   `{"command":"ls"}`,
		ApprovalID:  req.ID,
		InterruptID: "ic-test-1",
	})

	svc := &AppService{rt: rt}
	out := svc.ResolveApproval(req.ID, true, "approved in test")
	if out["success"] != true {
		t.Fatalf("resolve: %+v", out)
	}
	if _, ok := rt.pending.Get(req.ID); ok {
		t.Fatal("pending should be cleared after resolve")
	}
	if _, hasResume := out["resume_error"]; hasResume {
		t.Fatalf("unexpected resume without agent runner: %+v", out)
	}

	pending, _ := queue.ListPending(ctx, nil)
	if len(pending) != 0 {
		t.Fatalf("queue still has pending: %d", len(pending))
	}
}

func TestBridgeGovernancePendingResolveWithArguments(t *testing.T) {
	queue, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	rt := &Runtime{
		approvals: queue,
		pending:   newPendingStore(),
	}
	ctx := context.Background()

	req := governance.NewApprovalRequest(
		string(governance.KindToolApproval),
		"test",
		"Approve execute_bash",
		"high risk",
	)
	req.Fingerprint = governance.ComputePayloadHash("execute_bash", `{"command":"rm -rf /"}`)
	if err := queue.CreateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}

	rt.pending.Set(pendingRun{
		SessionID:   "sess-1",
		ToolName:    "execute_bash",
		Arguments:   `{"command":"rm -rf /"}`,
		ApprovalID:  req.ID,
		InterruptID: "ic-test-1",
	})

	svc := &AppService{rt: rt}
	// override arguments to safe target
	overrideArgs := `{"command":"rm -rf /tmp"}`
	out := svc.ResolveApproval(req.ID, true, "modified command", overrideArgs)
	if out["success"] != true {
		t.Fatalf("resolve with args: %+v", out)
	}

	// Verify approvals queue resolved payload contains override arguments
	resolvedReq, err := queue.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedReq == nil || resolvedReq.ResumePayload == nil {
		t.Fatal("expected resolved approval with resume payload")
	}
	if resolvedReq.ResumePayload.Arguments != overrideArgs {
		t.Fatalf("expected arguments %q, got %q", overrideArgs, resolvedReq.ResumePayload.Arguments)
	}
}
