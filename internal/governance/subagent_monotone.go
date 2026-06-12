package governance

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

type subagentDepthKey struct{}

const maxSubagentDepth = 4

// WithSubagentDepth attaches delegation depth for monotone governance.
func WithSubagentDepth(ctx context.Context, depth int) context.Context {
	return context.WithValue(ctx, subagentDepthKey{}, depth)
}

func subagentDepthFrom(ctx context.Context) int {
	v, _ := ctx.Value(subagentDepthKey{}).(int)
	return v
}

// SubagentDepth returns current delegation depth from context.
func SubagentDepth(ctx context.Context) int {
	return subagentDepthFrom(ctx)
}

// SubagentMonotoneMiddleware blocks invoke_subagent when delegation depth exceeds limit.
type SubagentMonotoneMiddleware struct {
	adk.BaseChatModelAgentMiddleware
}

func NewSubagentMonotoneMiddleware() *SubagentMonotoneMiddleware {
	return &SubagentMonotoneMiddleware{}
}

func (m *SubagentMonotoneMiddleware) WrapInvokableToolCall(ctx context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	if tCtx == nil || tCtx.Name != "invoke_subagent" {
		return endpoint, nil
	}
	depth := subagentDepthFrom(ctx)
	if depth >= maxSubagentDepth {
		return func(context.Context, string, ...tool.Option) (string, error) {
			return "", fmt.Errorf("invoke_subagent blocked: depth %d >= %d", depth, maxSubagentDepth)
		}, nil
	}
	childCtx := WithSubagentDepth(ctx, depth+1)
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		return endpoint(childCtx, args, opts...)
	}, nil
}
