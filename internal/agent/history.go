package agent

import (
	"context"

	"github.com/cloudwego/eino/schema"
)

type historyKey struct{}

// WithHistory supplies the earlier turns of a conversation for ONE run.
//
// By default every AgentGo turn is a single user message and continuity comes
// from memory recall. Frontends that present a chat transcript (the Crush TUI)
// opt in to real history with this; runs without it behave exactly as before.
// History is plain user/assistant text, oldest first, excluding the new input.
func WithHistory(ctx context.Context, msgs []*schema.Message) context.Context {
	if len(msgs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, historyKey{}, msgs)
}

func historyFrom(ctx context.Context) []*schema.Message {
	h, _ := ctx.Value(historyKey{}).([]*schema.Message)
	return h
}

// runMessages is the message list for one run: history (if any) + the new input.
func runMessages(ctx context.Context, userText string) []*schema.Message {
	h := historyFrom(ctx)
	out := make([]*schema.Message, 0, len(h)+1)
	out = append(out, h...)
	return append(out, schema.UserMessage(userText))
}
