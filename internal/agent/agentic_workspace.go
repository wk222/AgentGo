package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/workspace"
)

// AgenticWorkspaceMiddleware injects SOUL/TEAM/rules into Agentic system messages.
type AgenticWorkspaceMiddleware struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.AgenticMessage]
	manager *workspace.WorkspaceManager
}

func NewAgenticWorkspaceMiddleware(rootDir string) *AgenticWorkspaceMiddleware {
	return &AgenticWorkspaceMiddleware{
		manager: workspace.NewWorkspaceManager(rootDir),
	}
}

func (m *AgenticWorkspaceMiddleware) BeforeModelRewriteState(
	ctx context.Context,
	state *adk.TypedChatModelAgentState[*schema.AgenticMessage],
	_ *adk.TypedModelContext[*schema.AgenticMessage],
) (context.Context, *adk.TypedChatModelAgentState[*schema.AgenticMessage], error) {
	wsContext := m.manager.BuildContext()
	if wsContext == "" {
		return ctx, state, nil
	}
	block := "\n\n[Workspace Context]:\n" + wsContext
	injected := false
	for i, msg := range state.Messages {
		if msg != nil && msg.Role == schema.AgenticRoleTypeSystem {
			state.Messages[i] = schema.SystemAgenticMessage(msg.String() + block)
			injected = true
			break
		}
	}
	if !injected {
		state.Messages = append([]*schema.AgenticMessage{
			schema.SystemAgenticMessage("[Workspace Context]:\n" + wsContext),
		}, state.Messages...)
	}
	return ctx, state, nil
}
