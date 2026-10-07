package bridge

import (
	"path/filepath"
	"testing"

	"agentgo/internal/engine"
)

type recEmitter struct {
	approvals []string
	tokens    []string
}

func (r *recEmitter) Status(string, map[string]any)           {}
func (r *recEmitter) Token(d string)                          { r.tokens = append(r.tokens, d) }
func (r *recEmitter) Reasoning(string)                        {}
func (r *recEmitter) ToolCall(string, string, string)         {}
func (r *recEmitter) ToolResult(string, string, string, bool) {}
func (r *recEmitter) FileChange(string, string, string, string) {
}
func (r *recEmitter) ApprovalRequest(id, tool, _, _ string) { r.approvals = append(r.approvals, id+":"+tool) }
func (r *recEmitter) Error(string)                          {}

var _ engine.Emitter = (*recEmitter)(nil)

func TestNewAppServiceRegistersEinoAsDefault(t *testing.T) {
	t.Setenv(crushEnvExe, filepath.Join(t.TempDir(), "no-such-crush")) // keep the opt-in sidecar out of this test
	s := NewAppService(nil)
	ids := s.ListEngines()
	if len(ids) != 1 || ids[0] != EinoEngineID {
		t.Fatalf("engines = %v", ids)
	}
	if s.Engines() == nil {
		t.Fatal("router is nil")
	}
}

func TestSendResultToEngine_Text(t *testing.T) {
	em := &recEmitter{}
	out := sendResultToEngine(SendMessageResult{Messages: []ChatMessageDTO{
		{Role: "assistant", Content: "hi"}, // empty Type defaults to text
	}}, em)
	if out.Pending || out.Error != "" || len(out.Messages) != 1 {
		t.Fatalf("out = %+v", out)
	}
	if m := out.Messages[0]; m.Type != "text" || m.Content != "hi" || m.Role != "assistant" {
		t.Fatalf("msg = %+v", m)
	}
	if len(em.approvals) != 0 {
		t.Fatalf("unexpected approvals %v", em.approvals)
	}
}

func TestSendResultToEngine_PendingApprovalAndQuestion(t *testing.T) {
	em := &recEmitter{}
	out := sendResultToEngine(SendMessageResult{Messages: []ChatMessageDTO{
		{Role: "assistant", Type: "approval", ApprovalID: "a1", ToolName: "bash", Arguments: "{}", Status: "pending", Content: "等待审批: bash"},
		{Role: "assistant", Type: "question", ApprovalID: "q1", ToolName: "ask_user", Status: "pending", Content: "?"},
		{Role: "assistant", Type: "approval", ApprovalID: "a2", ToolName: "x", Status: "approved"}, // not pending
	}}, em)
	if !out.Pending || len(out.Messages) != 3 {
		t.Fatalf("out = %+v", out)
	}
	if len(em.approvals) != 2 || em.approvals[0] != "a1:bash" || em.approvals[1] != "q1:ask_user" {
		t.Fatalf("approvals = %v", em.approvals)
	}
}

func TestSendResultToEngine_Error(t *testing.T) {
	out := sendResultToEngine(SendMessageResult{Error: "boom"}, &recEmitter{})
	if out.Error != "boom" || len(out.Messages) != 0 {
		t.Fatalf("out = %+v", out)
	}
}
