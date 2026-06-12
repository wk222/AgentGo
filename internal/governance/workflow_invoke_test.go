package governance

import (
	"context"
	"testing"
)

func TestInvokeWithPolicyInterruptsHighRiskTool(t *testing.T) {
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	policy := Policy{ToolRiskLevels: map[string]RiskLevel{"execute_bash": RiskCritical}}
	mw := NewGovernanceMiddleware(queue, policy)
	ctx := context.Background()

	called := false
	_, err = mw.InvokeWithPolicy(ctx, "execute_bash", `{"command":"ls"}`, func(context.Context, string) (string, error) {
		called = true
		return "ok", nil
	})
	if err == nil {
		t.Fatal("expected interrupt")
	}
	if called {
		t.Fatal("endpoint should not run before approval")
	}
}
