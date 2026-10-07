package bridge

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"

	"agentgo/internal/sessions"
)

const (
	historyMaxMessages = 24
	historyMaxRunes    = 24000 // rough budget; oldest turns are dropped first
)

// buildHistory turns stored session messages into chat history: plain
// user/assistant text only (approval/question rows and tool traffic are not
// replayed), newest turns kept within the budget, oldest first.
func buildHistory(msgs []sessions.Message, maxMsgs, maxRunes int) []*schema.Message {
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

// sessionHistory loads the stored transcript of an AgentGo session. Call it
// BEFORE runEngineTurn, which appends the new user message to the same store.
func (s *AppService) sessionHistory(ctx context.Context, sessionID string) []*schema.Message {
	if sessionID == "" || s.rt == nil || s.rt.Sessions() == nil {
		return nil
	}
	msgs, err := s.rt.Sessions().GetMessages(ctx, sessionID, 500)
	if err != nil {
		return nil
	}
	return buildHistory(msgs, historyMaxMessages, historyMaxRunes)
}
