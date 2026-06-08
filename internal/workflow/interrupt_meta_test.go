package workflow

import (
	"testing"

	"github.com/cloudwego/eino/compose"

	"agentgo/internal/governance"
)

func TestMetaFromInfoToolApproval(t *testing.T) {
	info := &compose.InterruptInfo{
		InterruptContexts: []*compose.InterruptCtx{{
			ID:         "ic-tool-1",
			IsRootCause: true,
			Info: governance.ToolApprovalPause{
				ApprovalID: "appr-1",
				ToolName:   "execute_bash",
				Arguments:  `{"command":"echo hi"}`,
			},
		}},
	}
	meta := MetaFromInfo(info)
	if meta.Kind != InterruptToolApproval {
		t.Fatalf("kind=%s", meta.Kind)
	}
	if meta.ApprovalID != "appr-1" || meta.ToolName != "execute_bash" {
		t.Fatalf("meta=%+v", meta)
	}
	if meta.InterruptID != "ic-tool-1" {
		t.Fatalf("interrupt_id=%s", meta.InterruptID)
	}
}

func TestMetaFromInfoWaitSignal(t *testing.T) {
	info := &compose.InterruptInfo{
		InterruptContexts: []*compose.InterruptCtx{{
			ID:   "ic-wait",
			Info: map[string]any{"signal": "go"},
		}},
	}
	meta := MetaFromInfo(info)
	if meta.Kind != InterruptWaitSignal || meta.Signal != "go" {
		t.Fatalf("meta=%+v", meta)
	}
}
