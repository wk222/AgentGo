package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// BeforeModelRewriteState runs before every model call of a run and the state keeps the
// rewritten message, so appending the mode hint blindly grew the system prompt per call.
func TestModeProfileMiddlewareIsIdempotent(t *testing.T) {
	mw := NewModeProfileMiddleware(DefaultSessionMode())
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("q")}}

	_, state, err := mw.BeforeModelRewriteState(context.Background(), state, nil)
	if err != nil {
		t.Fatal(err)
	}
	once := state.Messages[0].Content
	for i := 0; i < 5; i++ {
		if _, state, err = mw.BeforeModelRewriteState(context.Background(), state, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := state.Messages[0].Content; got != once {
		t.Fatalf("system message grew across calls:\nonce: %d chars\nnow : %d chars", len(once), len(got))
	}
}
