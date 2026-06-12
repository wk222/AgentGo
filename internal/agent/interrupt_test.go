package agent

import (
	"testing"

	"github.com/cloudwego/eino/adk"

	"agentgo/internal/governance"
)

func TestPickInterruptPause_GovernanceState(t *testing.T) {
	id, pause := pickInterruptPause([]*adk.InterruptCtx{{
		ID: "ic-1", IsRootCause: true,
		Info: governance.ToolApprovalPause{ApprovalID: "appr-9", ToolName: "execute_bash", Arguments: `{"cmd":"ls"}`},
	}})
	if id != "ic-1" || pause == nil {
		t.Fatalf("unexpected: id=%s pause=%v", id, pause)
	}
	if pause.ApprovalID != "appr-9" || pause.InterruptID != "ic-1" || pause.ToolName != "execute_bash" {
		t.Fatalf("pause: %+v", pause)
	}
}

func TestParseInterruptPause_ApprovalMap(t *testing.T) {
	p := parseInterruptPause(map[string]any{
		"tool": "execute_bash", "approval_id": "appr-2", "arguments": "{}",
	}, "ic-2")
	if p.ApprovalID != "appr-2" || p.InterruptID != "ic-2" {
		t.Fatalf("%+v", p)
	}
}
