package agent

import (
	"context"
	"strconv"
	"sync/atomic"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// toolCallSeq numbers tool calls whose id the framework did not provide.
var toolCallSeq atomic.Uint64

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
	toolName, callID := "", ""
	if tc != nil {
		toolName, callID = tc.Name, tc.CallID
	}
	return func(innerCtx context.Context, args string, opts ...tool.Option) (string, error) {
		// ADK does not always supply the model's call id here; observers pair
		// start/finish by id, so one is always guaranteed.
		callID := callID
		if callID == "" {
			callID = compose.GetToolCallID(innerCtx)
		}
		if callID == "" {
			callID = "call_" + strconv.FormatUint(toolCallSeq.Add(1), 10)
		}
		EmitTrace(innerCtx, "start", "Tool", toolName, toolName)
		obs := toolObserverFrom(innerCtx)
		if obs != nil {
			obs.ToolStarted(callID, toolName, args)
		}
		result, err := endpoint(innerCtx, args, opts...)
		if err != nil {
			EmitTrace(innerCtx, "error", "Tool", toolName, err.Error())
			if obs != nil {
				obs.ToolFinished(callID, toolName, err.Error(), true)
			}
		} else {
			EmitTrace(innerCtx, "end", "Tool", toolName, "")
			if obs != nil {
				obs.ToolFinished(callID, toolName, result, false)
			}
		}
		return result, err
	}, nil
}
