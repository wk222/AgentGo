package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

// ToolCallEventMiddleware emits agent:trace events when a tool call starts and ends,
// so the Wails frontend can display "Calling <tool>..." in the status bar.
type ToolCallEventMiddleware struct {
	adk.BaseChatModelAgentMiddleware
}

// NewToolCallEventMiddleware creates the middleware. It is always enabled.
func NewToolCallEventMiddleware() adk.ChatModelAgentMiddleware {
	return &ToolCallEventMiddleware{}
}

func (m *ToolCallEventMiddleware) WrapInvokableToolCall(
	ctx context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	tc *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	toolName := ""
	if tc != nil {
		toolName = tc.Name
	}
	return func(innerCtx context.Context, args string, opts ...tool.Option) (string, error) {
		EmitTrace(innerCtx, "start", "Tool", toolName, toolName)
		result, err := endpoint(innerCtx, args, opts...)
		if err != nil {
			EmitTrace(innerCtx, "error", "Tool", toolName, err.Error())
		} else {
			EmitTrace(innerCtx, "end", "Tool", toolName, "")
		}
		return result, err
	}, nil
}
