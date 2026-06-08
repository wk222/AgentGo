package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	"github.com/cloudwego/eino/adk/middlewares/patchtoolcalls"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/governance"
	"agentgo/internal/sessions"
)

// BuildADKHandlers wires workspace/memory, official Eino middlewares, and optional toolsearch.
// MemoryMiddleware.AfterAgent runs here on successful ADK termination (framework applyAfterAgent).
func (r *Runner) BuildADKHandlers(ctx context.Context, chatModel model.ToolCallingChatModel) ([]adk.ChatModelAgentMiddleware, error) {
	stack, err := BuildTypedMiddlewareStack[*schema.Message](ctx, chatModel, r.workspaceRoot, r.dataDir)
	if err != nil {
		return nil, err
	}

	var handlers []adk.ChatModelAgentMiddleware
	handlers = append(handlers, NewModeProfileMiddleware(r.SessionMode()))
	if r.wsMW != nil {
		handlers = append(handlers, r.wsMW)
	}
	if r.memMW != nil {
		handlers = append(handlers, r.memMW)
	}
	handlers = append(handlers, stack...)

	if r.toolReg != nil {
		sm := SessionModeFromContext(ctx)
		dyn := RegistryDynamicToolsForMode(r.toolReg, sm)
		if len(dyn) > 0 {
			ts, err := toolsearch.New(ctx, &toolsearch.Config{DynamicTools: dyn})
			if err != nil {
				return handlers, err
			}
			handlers = append(handlers, ts)
		}
	}
	if r.queue != nil {
		handlers = append(handlers, governance.NewGovernanceMiddleware(r.queue, r.effectivePolicy()))
	}
	handlers = append(handlers, governance.NewSubagentMonotoneMiddleware())

	// P1: patchtoolcalls repairs dangling tool calls in message history (missing tool responses).
	// This complements tool_heal (which repairs malformed JSON args) — they fix different problems.
	if ptc, ptcErr := patchtoolcalls.New(ctx, nil); ptcErr == nil {
		handlers = append(handlers, ptc)
	}

	if hm := NewADKToolHealMiddleware(); hm != nil {
		handlers = append(handlers, hm)
	}

	// P2: tool call event emitter — broadcasts tool:call events to Wails frontend.
	handlers = append(handlers, NewToolCallEventMiddleware())

	// Session spine: six-section projection immediately before the model (innermost before SafeTool).
	var taskProvider func(context.Context) []string
	if r.adminRunner != nil {
		taskProvider = r.adminRunner.GetActiveTaskGoals
	}
	var kernel *sessions.SessionKernel
	if strings.TrimSpace(r.workspaceRoot) != "" {
		kernel = sessions.NewSessionKernel(r.workspaceRoot, 8, 45)
	}
	handlers = append(handlers, NewSessionSpineMiddleware(SessionSpineConfig{
		WorkspaceRoot:      r.workspaceRoot,
		Kernel:             kernel,
		EpisodicCompressor: r.episodicCompressor,
		GetDurableTasks:    taskProvider,
		MaxActiveTurns:     maxActiveTurnsFor(ctx),
	}))

	// Ch.05: innermost — tool errors become [tool error] strings for the model.
	handlers = append(handlers, NewSafeToolMiddleware())
	return handlers, nil
}
