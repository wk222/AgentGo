package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// safeToolMiddleware converts tool errors to model-readable strings (Eino Ch.05).
// Place innermost (last in Handlers) so interrupt errors still propagate.
type safeToolMiddleware[M adk.MessageType] struct {
	adk.TypedBaseChatModelAgentMiddleware[M]
}

func NewSafeToolMiddleware() adk.ChatModelAgentMiddleware {
	return &safeToolMiddleware[*schema.Message]{}
}

func NewTypedSafeToolMiddleware[M adk.MessageType]() adk.TypedChatModelAgentMiddleware[M] {
	return &safeToolMiddleware[M]{}
}

func (m *safeToolMiddleware[M]) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		result, err := endpoint(ctx, args, opts...)
		if err != nil {
			if _, ok := compose.IsInterruptRerunError(err); ok {
				return "", err
			}
			return fmt.Sprintf("[tool error] %v", err), nil
		}
		return result, nil
	}, nil
}

func (m *safeToolMiddleware[M]) WrapStreamableToolCall(
	_ context.Context,
	endpoint adk.StreamableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.StreamableToolCallEndpoint, error) {
	return func(ctx context.Context, args string, opts ...tool.Option) (*schema.StreamReader[string], error) {
		sr, err := endpoint(ctx, args, opts...)
		if err != nil {
			if _, ok := compose.IsInterruptRerunError(err); ok {
				return nil, err
			}
			return schema.StreamReaderFromArray([]string{fmt.Sprintf("[tool error] %v", err)}), nil
		}
		return sr, nil
	}, nil
}
