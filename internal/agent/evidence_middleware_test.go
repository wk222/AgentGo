package agent

import (
	"agentgo/internal/evidence"
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func checkEvidenceMiddleware[M adk.MessageType](t *testing.T) {
	t.Helper()
	mw := &evidenceMiddleware[M]{}
	original := &adk.TypedChatModelAgentContext[M]{Instruction: "original"}
	_, next, err := mw.BeforeAgent(context.Background(), original)
	if err != nil || next == original || original.Instruction != "original" || !strings.Contains(next.Instruction, "[[ev_ID]]") {
		t.Fatalf("bad instruction injection: %+v %v", next, err)
	}
	_, twice, err := mw.BeforeAgent(context.Background(), next)
	if err != nil || twice.Instruction != next.Instruction {
		t.Fatal("duplicated instructions on continuation")
	}
}

func TestEvidenceMiddlewareBothMessageFamilies(t *testing.T) {
	checkEvidenceMiddleware[*schema.Message](t)
	checkEvidenceMiddleware[*schema.AgenticMessage](t)
}

func TestBindRunContextEvidenceScope(t *testing.T) {
	ctx := BindRunContext(context.Background(), "")
	if SessionIDFromContext(ctx) != "desktop" {
		t.Fatal("default session changed")
	}
	c := evidence.Register(ctx, "desktop", evidence.Citation{Kind: "file", Source: "a", Revision: "r", End: 2}, "中文")
	if c == nil {
		t.Fatal("agent/tool default evidence scopes differ")
	}
	if _, err := evidence.Read(BindRunContext(ctx, "other"), "other", c.ID); err == nil {
		t.Fatal("session switch inherited evidence")
	}
}
