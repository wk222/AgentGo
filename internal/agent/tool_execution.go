package agent

import (
	"context"
	"fmt"

	"agentgo/internal/governance"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// One execution contract for chat, specialists and prebuilt agents. Errors
// remain outside observers/governance so they see the real outcome. Chat can
// place context middleware between these phases; explicit tool lists stay fixed.
type toolExecutionHandlers struct {
	errors adk.ChatModelAgentMiddleware
	calls  []adk.ChatModelAgentMiddleware
}

func (r *Runner) toolExecutionHandlers(ctx context.Context) toolExecutionHandlers {
	h := toolExecutionHandlers{errors: NewSafeToolMiddleware()}
	// Ordering waits come before repair and approval, so prompts for a reply's
	// calls also appear in the order the model listed them.
	if barrier := NewToolBarrierMiddleware(); barrier != nil {
		h.calls = append(h.calls, barrier)
	}
	if heal := NewADKToolHealMiddleware(); heal != nil {
		h.calls = append(h.calls, heal)
	}
	if collect := newBGCollectMiddleware(r.bgWake); collect != nil {
		h.calls = append(h.calls, collect)
	}
	if r.queue != nil {
		h.calls = append(h.calls, governance.NewGovernanceMiddleware(r.queue, r.effectivePolicyForCtx(ctx)))
	}
	h.calls = append(h.calls, governance.NewSubagentMonotoneMiddleware(), NewToolCallEventMiddleware())
	return h
}

func (h toolExecutionHandlers) all() []adk.ChatModelAgentMiddleware {
	return append([]adk.ChatModelAgentMiddleware{h.errors}, h.calls...)
}

// The pinned planexecute.ExecutorConfig has no Handlers field. Adapt the same
// ADK endpoints at that boundary; there is no alternate policy/heal pipeline.
func (h toolExecutionHandlers) compose() compose.ToolMiddleware {
	return compose.ToolMiddleware{Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			if input == nil {
				return nil, fmt.Errorf("tool execution: nil input")
			}
			endpoint := adk.InvokableToolCallEndpoint(func(ctx context.Context, args string, _ ...tool.Option) (string, error) {
				out, err := next(ctx, cloneToolInput(input, args))
				if err != nil {
					return "", err
				}
				if out == nil {
					return "", nil
				}
				return out.Result, nil
			})
			chain := h.all()
			for i := len(chain) - 1; i >= 0; i-- {
				var err error
				endpoint, err = chain[i].WrapInvokableToolCall(ctx, endpoint, &adk.ToolContext{Name: input.Name, CallID: input.CallID})
				if err != nil {
					return nil, err
				}
			}
			out, err := endpoint(ctx, input.Arguments)
			if err != nil {
				return nil, err
			}
			return &compose.ToolOutput{Result: out}, nil
		}
	}}
}
