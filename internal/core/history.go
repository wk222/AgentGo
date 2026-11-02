// Package core is AgentGo's kernel: the business logic every entry point
// (desktop, HTTP gateway, ACP, terminal UI) shares. Entry points translate
// their protocol into calls on core; they hold no chat logic of their own.
package core

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"

	"agentgo/internal/sessions"
)

const (
	// HistoryMaxMessages caps how many past messages are replayed to the model.
	HistoryMaxMessages = 24
	// HistoryMaxRunes is a rough budget; the oldest turns are dropped first.
	HistoryMaxRunes = 24000
)

// BuildHistory turns stored session messages into chat history: plain
// user/assistant text only (approval/question rows and tool traffic are not
// replayed), newest turns kept within the budget, oldest first.
func BuildHistory(msgs []sessions.Message, maxMsgs, maxRunes int) []*schema.Message {
	var picked []*schema.Message
	total := 0
	for i := len(msgs) - 1; i >= 0 && len(picked) < maxMsgs; i-- {
		m := msgs[i]
		if (m.Type != "" && m.Type != "text") || strings.TrimSpace(m.Content) == "" {
			continue
		}
		var msg *schema.Message
		switch m.Role {
		case "user":
			msg = schema.UserMessage(m.Content)
		case "assistant":
			msg = schema.AssistantMessage(m.Content, nil)
		default:
			continue
		}
		n := len([]rune(m.Content))
		if total+n > maxRunes && len(picked) > 0 {
			break
		}
		total += n
		picked = append(picked, msg)
	}
	for i, j := 0, len(picked)-1; i < j; i, j = i+1, j-1 { // newest-first -> oldest-first
		picked[i], picked[j] = picked[j], picked[i]
	}
	// A transcript must not start with an assistant turn.
	for len(picked) > 0 && picked[0].Role != schema.User {
		picked = picked[1:]
	}
	return picked
}

// BuildMigrationHistory keeps the old prompt window plus a bounded recovery
// ledger from the broader pre-turn transcript. This runs once per native scope.
func BuildMigrationHistory(ctx context.Context, stored []sessions.Message) ([]*schema.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var all []*schema.Message
	for _, msg := range stored {
		if (msg.Type != "" && msg.Type != "text") || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		switch msg.Role {
		case "user":
			all = append(all, schema.UserMessage(msg.Content))
		case "assistant":
			all = append(all, schema.AssistantMessage(msg.Content, nil))
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	tail := BuildHistory(stored, HistoryMaxMessages, HistoryMaxRunes)
	window := max(1, len(tail))
	view := sessions.CompileProjectedView(all, sessions.CompileOptions{MaxActiveTurns: window, PreserveLatestUser: true})
	return view.Render(ctx)
}
