package governance

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func approvedInvocation(t *testing.T, q *ApprovalQueue, ctx context.Context, workspace string) {
	t.Helper()
	req := NewApprovalRequest(string(KindToolApproval), "agent_runtime", "test", "test")
	req.Status = "approved"
	req.Fingerprint = ComputePayloadHash("create_tool", `{"name":"demo"}`)
	req.Metadata = map[string]interface{}{"workspace": workspace}
	if err := q.CreateRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalCannotCrossSessionWorkspaceOrCall(t *testing.T) {
	for _, change := range []string{"session", "workspace", "call"} {
		t.Run(change, func(t *testing.T) {
			q, err := NewApprovalQueue(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer q.Close()
			ctx := WithCallID(WithSessionID(context.Background(), "a"), "call-a")
			approvedInvocation(t, q, ctx, "workspace-a")
			workspace := "workspace-a"
			switch change {
			case "session":
				ctx = WithSessionID(ctx, "b")
			case "workspace":
				workspace = "workspace-b"
			case "call":
				ctx = WithCallID(ctx, "call-b")
			}
			mw := NewGovernanceMiddleware(q, Policy{WorkspaceRoot: workspace, ToolRiskLevels: map[string]RiskLevel{"create_tool": RiskHigh}})
			ran := false
			_, _ = mw.InvokeWithPolicy(ctx, "create_tool", `{"name":"demo"}`, func(context.Context, string) (string, error) { ran = true; return "done", nil })
			if ran {
				t.Fatal("approval authorized a different invocation")
			}
		})
	}
}

func TestConcurrentApprovalConsumptionExecutesOnlyOnce(t *testing.T) {
	q, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	ctx := WithCallID(WithSessionID(context.Background(), "a"), "call-a")
	approvedInvocation(t, q, ctx, "workspace-a")
	mw := NewGovernanceMiddleware(q, Policy{WorkspaceRoot: "workspace-a", ToolRiskLevels: map[string]RiskLevel{"create_tool": RiskHigh}})
	var runs atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _ = mw.InvokeWithPolicy(ctx, "create_tool", `{"name":"demo"}`, func(context.Context, string) (string, error) { runs.Add(1); return "done", nil })
		}()
	}
	close(start)
	wg.Wait()
	if runs.Load() != 1 {
		t.Fatal("approval executed an unexpected number of times", runs.Load())
	}
}

func TestCancelledToolNeverRunsWithoutHooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mw := NewGovernanceMiddleware(nil, Policy{})
	called := false
	_, err := mw.InvokeWithPolicy(ctx, "code_view", "{}", func(context.Context, string) (string, error) { called = true; return "done", nil })
	if err != context.Canceled || called {
		t.Fatal("cancelled tool executed", err, called)
	}
}
