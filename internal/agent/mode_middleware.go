package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// ModeProfileMiddleware injects PyBot ModeProfile × ExecutionCanvas into system prompt.
type ModeProfileMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	defaultMode SessionMode
}

func NewModeProfileMiddleware(defaultMode SessionMode) *ModeProfileMiddleware {
	if defaultMode.Profile == "" && defaultMode.Canvas == "" {
		defaultMode = DefaultSessionMode()
	}
	return &ModeProfileMiddleware{defaultMode: defaultMode}
}

func (m *ModeProfileMiddleware) resolveMode(ctx context.Context) SessionMode {
	if sm, ok := sessionModeFromContext(ctx); ok {
		return sm
	}
	return m.defaultMode.Normalized()
}

func (m *ModeProfileMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	sm := m.resolveMode(ctx)
	if len(state.ToolInfos) > 0 {
		state.ToolInfos = filterToolInfos(state.ToolInfos, sm)
	}
	if len(state.DeferredToolInfos) > 0 {
		state.DeferredToolInfos = filterToolInfos(state.DeferredToolInfos, sm)
	}
	hint := sm.ModeHints()
	injected := false
	for i, msg := range state.Messages {
		if msg.Role == schema.System {
			state.Messages[i].Content = fmt.Sprintf("%s\n\n%s", msg.Content, hint)
			injected = true
			break
		}
	}
	if !injected {
		state.Messages = append([]*schema.Message{schema.SystemMessage(hint)}, state.Messages...)
	}
	return ctx, state, nil
}

// AgenticModeProfileMiddleware injects mode hints on the AgenticMessage ADK path.
type AgenticModeProfileMiddleware struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	defaultMode SessionMode
}

func NewAgenticModeProfileMiddleware(defaultMode SessionMode) *AgenticModeProfileMiddleware {
	if defaultMode.Profile == "" && defaultMode.Canvas == "" {
		defaultMode = DefaultSessionMode()
	}
	return &AgenticModeProfileMiddleware{defaultMode: defaultMode}
}

func (m *AgenticModeProfileMiddleware) resolveMode(ctx context.Context) SessionMode {
	if sm, ok := sessionModeFromContext(ctx); ok {
		return sm
	}
	return m.defaultMode.Normalized()
}

func (m *AgenticModeProfileMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	_ *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	sm := m.resolveMode(ctx)
	if len(state.ToolInfos) > 0 {
		state.ToolInfos = filterToolInfos(state.ToolInfos, sm)
	}
	hint := sm.ModeHints()
	injected := false
	for i, msg := range state.Messages {
		if msg != nil && msg.Role == schema.AgenticRoleTypeSystem {
			state.Messages[i] = schema.SystemAgenticMessage(msg.String() + "\n\n" + hint)
			injected = true
			break
		}
	}
	if !injected {
		state.Messages = append([]*schema.AgenticMessage{schema.SystemAgenticMessage(hint)}, state.Messages...)
	}
	return ctx, state, nil
}
