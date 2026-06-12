package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/memory"
)

// AgenticMemoryMiddleware injects memory on Typed AgenticMessage ADK path.
type AgenticMemoryMiddleware struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	engine memory.Engine
}

func NewAgenticMemoryMiddleware(engine memory.Engine) *AgenticMemoryMiddleware {
	return &AgenticMemoryMiddleware{engine: engine}
}

func (m *AgenticMemoryMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	_ *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	promptCtx, err := m.engine.ContextPrompt(ctx, SessionIDFromContext(ctx))
	if err != nil || promptCtx == "" {
		return ctx, state, nil
	}
	injected := false
	for i, msg := range state.Messages {
		if msg != nil && msg.Role == schema.AgenticRoleTypeSystem {
			state.Messages[i] = schema.SystemAgenticMessage(msg.String() + "\n\n[Injected Memory Context]:\n" + promptCtx)
			injected = true
			break
		}
	}
	if !injected {
		state.Messages = append([]*schema.AgenticMessage{
			schema.SystemAgenticMessage("[Injected Memory Context]:\n" + promptCtx),
		}, state.Messages...)
	}
	return ctx, state, nil
}

func (m *AgenticMemoryMiddleware) AfterAgent(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage]) (context.Context, error) {
	if len(state.Messages) == 0 {
		return ctx, nil
	}
	last := state.Messages[len(state.Messages)-1]
	if last == nil || last.Role != schema.AgenticRoleTypeAssistant {
		return ctx, nil
	}
	text := last.String()
	if text == "" {
		return ctx, nil
	}
	_ = m.engine.Ingest(ctx, memory.Record{
		ID:       fmt.Sprintf("turn_%d", time.Now().UnixNano()),
		Content:  text,
		Scope:    SessionIDFromContext(ctx),
		Modality: "episode",
		Status:   "active",
	})
	return ctx, nil
}
