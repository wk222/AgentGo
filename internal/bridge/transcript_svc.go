package bridge

import (
	"context"

	"github.com/cloudwego/eino/schema"

	"agentgo/internal/core"
)

// transcript returns the session transcript service (see core.Transcript).
func (s *AppService) transcript() *core.Transcript {
	return transcriptOf(s.rt)
}

// transcriptOf builds the transcript service for a runtime; persistence is
// disabled when the runtime has no session store.
func transcriptOf(rt *Runtime) *core.Transcript {
	if rt == nil || rt.Sessions() == nil {
		return core.NewTranscript(nil)
	}
	return core.NewTranscript(rt.Sessions())
}

// sessionHistory loads the stored transcript of an AgentGo session. Call it
// BEFORE the turn appends the new user message to the same store.
func (s *AppService) sessionHistory(ctx context.Context, sessionID string) []*schema.Message {
	return s.transcript().History(ctx, sessionID)
}

// coreMessages converts reply DTOs into persistable core messages.
func coreMessages(msgs []ChatMessageDTO) []core.Message {
	out := make([]core.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, core.Message{
			Role: m.Role, Content: m.Content, Type: m.Type,
			ApprovalID: m.ApprovalID, ToolName: m.ToolName, Arguments: m.Arguments, Status: m.Status,
		})
	}
	return out
}
