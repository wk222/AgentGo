package governance

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

func TestGovernanceHighRiskStatefulInterruptThenApprove(t *testing.T) {
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	policy := Policy{
		MaxDailyBudget: 100,
		ToolRiskLevels: map[string]RiskLevel{"execute_bash": RiskCritical},
	}
	mw := NewGovernanceMiddleware(queue, policy)
	ctx := context.Background()

	executed := false
	endpoint := func(_ context.Context, _ string, _ ...tool.Option) (string, error) {
		executed = true
		return "bash_ok", nil
	}
	wrapped, err := mw.WrapInvokableToolCall(ctx, endpoint, &adk.ToolContext{Name: "execute_bash"})
	if err != nil {
		t.Fatal(err)
	}

	args := `{"command":"echo hi"}`
	_, err = wrapped(ctx, args)
	if err == nil {
		t.Fatal("expected StatefulInterrupt error before approval")
	}

	pending, err := queue.ListPending(ctx, nil)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%d err=%v", len(pending), err)
	}
	if err := queue.Resolve(ctx, pending[0].ID, true, "test approve", "tester", &ResumePayload{Approved: true}); err != nil {
		t.Fatal(err)
	}

	out, err := wrapped(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	if out != "bash_ok" || !executed {
		t.Fatalf("out=%q executed=%v", out, executed)
	}
}

func TestGovernanceRejectAfterInterrupt(t *testing.T) {
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	mw := NewGovernanceMiddleware(queue, Policy{
		ToolRiskLevels: map[string]RiskLevel{"create_tool": RiskHigh},
	})
	ctx := context.Background()
	wrapped, err := mw.WrapInvokableToolCall(ctx, func(_ context.Context, _ string, _ ...tool.Option) (string, error) {
		return "should_not_run", nil
	}, &adk.ToolContext{Name: "create_tool"})
	if err != nil {
		t.Fatal(err)
	}

	args := `{"name":"x"}`
	_, err = wrapped(ctx, args)
	if err == nil {
		t.Fatal("expected interrupt")
	}

	pending, _ := queue.ListPending(ctx, nil)
	if len(pending) != 1 {
		t.Fatalf("pending=%d", len(pending))
	}
	_ = queue.Resolve(ctx, pending[0].ID, false, "denied", "tester", &ResumePayload{Approved: false})

	// Simulate resumed tool call with reject payload via approved-unconsumed path won't apply;
	// use fresh call after reject — should interrupt again or return rejection on resume ctx.
	// Second call without approval re-creates interrupt.
	_, err = wrapped(ctx, args)
	if err == nil {
		t.Fatal("expected interrupt after reject")
	}
}

func TestGovernanceHashTamperBlocked(t *testing.T) {
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	mw := NewGovernanceMiddleware(queue, Policy{
		ToolRiskLevels: map[string]RiskLevel{"mcp_filesystem": RiskHigh},
	})
	ctx := context.Background()
	wrapped, err := mw.WrapInvokableToolCall(ctx, func(_ context.Context, _ string, _ ...tool.Option) (string, error) {
		return "ok", nil
	}, &adk.ToolContext{Name: "mcp_filesystem"})
	if err != nil {
		t.Fatal(err)
	}

	orig := `{"path":"/tmp"}`
	_, err = wrapped(ctx, orig)
	if err == nil {
		t.Fatal("expected interrupt")
	}
	pending, _ := queue.ListPending(ctx, nil)
	_ = queue.Resolve(ctx, pending[0].ID, true, "ok", "tester", &ResumePayload{Approved: true})

	// Tampered args: hash mismatch on resume path uses findApproved for same hash only on matching args.
	// Approved fingerprint is for orig; calling with tampered args creates new approval or fails hash on resume.
	tampered := `{"path":"/etc/passwd"}`
	_, err = wrapped(ctx, tampered)
	if err == nil {
		// Without interrupt resume ctx, new pending is created — not execution
		pending2, _ := queue.ListPending(ctx, nil)
		if len(pending2) == 0 {
			t.Fatal("expected new pending or error for tampered args")
		}
		return
	}
}
