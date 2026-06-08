package governance

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/compose"
)

func TestComposeToolMiddlewareApprovalThenExecute(t *testing.T) {
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })

	policy := DefaultPolicy()
	mw := ComposeToolMiddleware(queue, policy)
	ctx := context.Background()

	executed := false
	next := func(_ context.Context, in *compose.ToolInput) (*compose.ToolOutput, error) {
		executed = true
		return &compose.ToolOutput{Result: "ok:" + in.Arguments}, nil
	}
	wrapped := mw.Invokable(next)

	in := &compose.ToolInput{Name: "execute_bash", Arguments: `{"command":"echo hi"}`}
	_, err = wrapped(ctx, in)
	if err == nil {
		t.Fatal("expected StatefulInterrupt before approval")
	}
	if executed {
		t.Fatal("should not execute before approval")
	}

	pending, _ := queue.ListPending(ctx, nil)
	if len(pending) != 1 {
		t.Fatalf("pending=%d", len(pending))
	}
	if err := queue.Resolve(ctx, pending[0].ID, true, "ok", "tester", &ResumePayload{Approved: true}); err != nil {
		t.Fatal(err)
	}

	out2, err := wrapped(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !executed || out2 == nil || out2.Result == "" {
		t.Fatalf("executed=%v out=%v", executed, out2)
	}
}
