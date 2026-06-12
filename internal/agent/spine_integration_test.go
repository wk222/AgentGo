package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/sessions"
)

func TestSessionSpineMiddlewareProjectsMessages(t *testing.T) {
	mw := NewSessionSpineMiddleware(SessionSpineConfig{
		WorkspaceRoot:  t.TempDir(),
		MaxActiveTurns: 10,
	})
	state := &adk.ChatModelAgentState{
		Messages: []*schema.Message{
			schema.UserMessage("hello"),
			schema.AssistantMessage("world", nil),
		},
	}
	ctx := context.Background()
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Messages) == 0 {
		t.Fatal("empty projected messages")
	}
	view := sessions.CompileProjectedView(state.Messages, sessions.CompileOptions{MaxActiveTurns: 10})
	if len(view.ActiveTurn) != 2 {
		t.Fatalf("active turns=%d", len(view.ActiveTurn))
	}
}
