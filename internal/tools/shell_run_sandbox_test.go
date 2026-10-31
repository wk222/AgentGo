package tools

import (
	"context"
	"strings"
	"testing"

	"agentgo/internal/sandbox"
)

// 开了 AGENTGO_SANDBOX_OS 而本机无法约束时必须拒绝执行，不能悄悄无约束运行。
func TestRunShellFailsClosedWhenOSSandboxUnavailable(t *testing.T) {
	if sandbox.Available() {
		t.Skip("OS sandbox is available here")
	}
	t.Setenv("AGENTGO_SANDBOX_OS", "1")
	out, err := runShell(context.Background(), t.TempDir(), "echo should-not-run", 0)
	if err == nil {
		t.Fatalf("expected a refusal, got output %+v", out)
	}
	if !strings.Contains(err.Error(), "AGENTGO_SANDBOX_OS") || strings.Contains(out.Stdout, "should-not-run") {
		t.Fatalf("unexpected result: err=%v out=%+v", err, out)
	}
}
