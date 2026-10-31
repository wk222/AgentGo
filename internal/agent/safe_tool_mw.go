package agent

import (
	"context"
	"errors"
	"strconv"
	"time"

	"agentgo/internal/governance"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// safeToolMiddleware converts tool errors to model-readable strings (Eino Ch.05).
// Place outside governance and observers so they see real execution errors.
// It also keeps the same-failure count and marks failures whose outcome is unknown
// (tool_failure_guard.go).
type safeToolMiddleware[M adk.MessageType] struct {
	adk.TypedBaseChatModelAgentMiddleware[M]
	guard *failureGuard
}

func NewSafeToolMiddleware() adk.ChatModelAgentMiddleware {
	return &safeToolMiddleware[*schema.Message]{guard: newFailureGuard()}
}

func NewTypedSafeToolMiddleware[M adk.MessageType]() adk.TypedChatModelAgentMiddleware[M] {
	return &safeToolMiddleware[M]{guard: newFailureGuard()}
}

func (m *safeToolMiddleware[M]) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	tc *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	name, callID := "", ""
	if tc != nil {
		name, callID = tc.Name, tc.CallID
	}
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		callID := callID // this call's own copy
		traced := CallTraceEnabled()
		var began time.Time
		if traced {
			// Everything inside this call (repair, policy) files its steps under the same id.
			id := callID
			if id == "" {
				id = compose.GetToolCallID(ctx)
			}
			if id == "" {
				id = "call_" + strconv.FormatUint(toolCallSeq.Add(1), 10)
			}
			callID = id
			ctx = governance.WithCallID(withTraceCallID(ctx, id), id)
			began = time.Now()
			recordCallTrace(ctx, id, name, TraceStageModelArgs, 0, map[string]any{"args": args})
		}
		if refusal, stop := m.guard.before(name, args); stop {
			if traced {
				recordCallTrace(ctx, callID, name, TraceStageResult, time.Since(began), map[string]any{"refused_by": "same_failure_guard", "result": refusal})
			}
			return refusal, nil
		}
		result, err := endpoint(ctx, args, opts...)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				if traced {
					recordCallTrace(ctx, callID, name, TraceStageResult, time.Since(began), map[string]any{"error": err.Error(), "aborted": true})
				}
				return "", err
			}
			if _, ok := compose.IsInterruptRerunError(err); ok {
				if traced {
					recordCallTrace(ctx, callID, name, TraceStageResult, time.Since(began), map[string]any{"paused": true})
				}
				return "", err
			}
			out := m.guard.after(name, args, failureText(err), true)
			if traced {
				recordCallTrace(ctx, callID, name, TraceStageResult, time.Since(began), map[string]any{"failed": true, "error": err.Error(), "result": out})
			}
			return out, nil
		}
		out := m.guard.after(name, args, result, resultFailed(result))
		if traced {
			recordCallTrace(ctx, callID, name, TraceStageResult, time.Since(began), map[string]any{"failed": resultFailed(result), "result": out, "result_chars": len([]rune(result))})
		}
		return out, nil
	}, nil
}

func (m *safeToolMiddleware[M]) WrapStreamableToolCall(
	_ context.Context,
	endpoint adk.StreamableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.StreamableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sr, err := endpoint(ctx, args, opts...)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			if _, ok := compose.IsInterruptRerunError(err); ok {
				return nil, err
			}
			return schema.StreamReaderFromArray([]string{failureText(err)}), nil
		}
		return sr, nil
	}, nil
}
