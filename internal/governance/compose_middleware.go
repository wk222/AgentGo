package governance

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
)

// ComposeToolMiddleware wraps compose ToolsNode invocations with the same policy as ADK (InvokeWithPolicy).
func ComposeToolMiddleware(queue *ApprovalQueue, policy Policy) compose.ToolMiddleware {
	mw := NewGovernanceMiddleware(queue, policy)
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if input == nil {
					return nil, fmt.Errorf("governance: nil tool input")
				}
				result, err := mw.InvokeWithPolicy(ctx, input.Name, input.Arguments, func(ctx context.Context, args string) (string, error) {
					out, invokeErr := next(ctx, &compose.ToolInput{Name: input.Name, Arguments: args})
					if invokeErr != nil {
						return "", invokeErr
					}
					if out == nil {
						return "", nil
					}
					return out.Result, nil
				})
				if err != nil {
					return nil, err
				}
				return &compose.ToolOutput{Result: result}, nil
			}
		},
	}
}
