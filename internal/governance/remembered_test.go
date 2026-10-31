package governance

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

func rememberedHarness(t *testing.T, risk RiskLevel, name string) (wrapped func(context.Context, string) (string, error), runs *int) {
	t.Helper()
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	mw := NewGovernanceMiddleware(queue, Policy{MaxDailyBudget: 100, ToolRiskLevels: map[string]RiskLevel{name: risk}})
	n := 0
	w, err := mw.WrapInvokableToolCall(context.Background(), func(_ context.Context, _ string, _ ...tool.Option) (string, error) {
		n++
		return "ran", nil
	}, &adk.ToolContext{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return func(ctx context.Context, args string) (string, error) { return w(ctx, args) }, &n
}

func TestRememberedCallSkipsApprovalOnlyForTheSameCallInTheSameSession(t *testing.T) {
	wrapped, runs := rememberedHarness(t, RiskHigh, "mcp_filesystem")
	ctxA := WithSessionID(context.Background(), "sess-A")
	ctxB := WithSessionID(context.Background(), "sess-B")
	args := `{"path":"/tmp/a"}`
	t.Cleanup(func() { ForgetRemembered("sess-A"); ForgetRemembered("sess-B") })

	if _, err := wrapped(ctxA, args); err == nil {
		t.Fatal("first call must pause for approval")
	}
	if err := RememberApproval("sess-A", "mcp_filesystem", args, ScopeCall); err != nil {
		t.Fatal(err)
	}
	if out, err := wrapped(ctxA, args); err != nil || out != "ran" || *runs != 1 {
		t.Fatalf("remembered call should run: out=%q err=%v runs=%d", out, err, *runs)
	}
	if _, err := wrapped(ctxA, `{"path":"/tmp/other"}`); err == nil {
		t.Fatal("a different call must still ask")
	}
	if _, err := wrapped(ctxB, args); err == nil {
		t.Fatal("another session must still ask")
	}
}

func TestRememberedToolCoversAllCallsOfThatTool(t *testing.T) {
	wrapped, runs := rememberedHarness(t, RiskHigh, "mcp_filesystem")
	ctx := WithSessionID(context.Background(), "sess-T")
	t.Cleanup(func() { ForgetRemembered("sess-T") })
	if err := RememberApproval("sess-T", "mcp_filesystem", "", ScopeTool); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{`{"path":"/a"}`, `{"path":"/b"}`} {
		if _, err := wrapped(ctx, a); err != nil {
			t.Fatalf("tool-wide remembered call %s: %v", a, err)
		}
	}
	if *runs != 2 {
		t.Fatalf("runs = %d", *runs)
	}
	ForgetRemembered("sess-T")
	if _, err := wrapped(ctx, `{"path":"/c"}`); err == nil {
		t.Fatal("forgetting must restore the prompt")
	}
}

func TestCriticalCallsAreNeverShortCircuited(t *testing.T) {
	wrapped, runs := rememberedHarness(t, RiskCritical, "execute_bash")
	ctx := WithSessionID(context.Background(), "sess-C")
	t.Cleanup(func() { ForgetRemembered("sess-C") })
	args := `{"command":"rm -rf x"}`
	if err := RememberApproval("sess-C", "execute_bash", args, ScopeCall); err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped(ctx, args); err == nil || *runs != 0 {
		t.Fatalf("a CRITICAL call must still pause (err=%v runs=%d)", err, *runs)
	}
}

func TestBroadToolsRefuseToolWideRemember(t *testing.T) {
	for _, name := range []string{"execute_bash", "run_elevated", "computer_use", ElevatedTool} {
		err := RememberApproval("s", name, "{}", ScopeTool)
		if err == nil || !strings.Contains(err.Error(), "too broad") {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if err := RememberApproval("", "x", "{}", ScopeCall); err == nil {
		t.Fatal("a session is required")
	}
}

func TestParseRememberScope(t *testing.T) {
	for in, want := range map[string]RememberScope{"": ScopeCall, "call": ScopeCall, "tool": ScopeTool, "Always": ScopeTool} {
		if got, err := ParseRememberScope(in); err != nil || got != want {
			t.Errorf("%q -> %v %v", in, got, err)
		}
	}
	if _, err := ParseRememberScope("forever"); err == nil {
		t.Fatal("unknown scope must error")
	}
}
