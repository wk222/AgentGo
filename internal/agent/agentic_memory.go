package agent

import (
	"agentgo/internal/config"
	"agentgo/internal/sessions"
	"context"
	"log"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/memory"
	"agentgo/internal/promptbudget"
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
	if m == nil || m.engine == nil || state == nil {
		return ctx, state, nil
	}
	if err := ctx.Err(); err != nil {
		return ctx, state, err
	}
	header := "[Injected Memory Context]:\n"
	ctx = memory.WithPromptBudget(ctx, max(0, memory.AutomaticTokenBudget()-promptbudget.Estimate(header)))
	promptCtx, err := m.engine.ContextPrompt(ctx, SessionIDFromContext(ctx))
	if ctx.Err() != nil {
		return ctx, state, ctx.Err()
	}
	if err != nil {
		promptCtx = ""
	}
	promptCtx = promptbudget.Excerpt(promptCtx, memory.PromptTokenBudget(ctx))
	out := make([]*schema.AgenticMessage, 0, len(state.Messages)+1)
	for _, msg := range state.Messages {
		if msg != nil && msg.Extra["agentgo.memory_context"] == true {
			continue
		}
		out = append(out, msg)
	}
	if promptCtx != "" {
		msg := schema.SystemAgenticMessage(header + promptCtx)
		msg.Extra = map[string]any{"agentgo.memory_context": true, "_eino_adk_runtime_generated_system_message": true}
		out = append([]*schema.AgenticMessage{msg}, out...)
	}
	state.Messages = out
	return ctx, state, nil
}

func (m *AgenticMemoryMiddleware) AfterAgent(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.AgenticMessage]) (context.Context, error) {
	if m == nil || m.engine == nil || state == nil || len(state.Messages) == 0 {
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
	if config.AutoLearn.On() {
		for i := len(state.Messages) - 1; i >= 0; i-- {
			msg := state.Messages[i]
			if msg != nil && msg.Role == schema.AgenticRoleTypeUser && msg.Extra["agentgo.memory_context"] != true && msg.Extra[sessions.ExtraContextKind] == nil {
				if p := memory.PipelineFromEngine(m.engine); p != nil {
					if _, err := p.QueueLearning(ctx, SessionIDFromContext(ctx), "user_turn", msg.String(), "", ""); err != nil {
						log.Printf("[memory] queue typed learning: %v", err)
					}
				}
				break
			}
		}
	}
	if record, ok := assistantEpisodeRecord(SessionIDFromContext(ctx), text, "agentic_after_agent", nil); ok {
		_ = m.engine.Ingest(ctx, record)
	}
	return ctx, nil
}
