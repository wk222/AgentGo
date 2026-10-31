package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// BuildADKHandlers wires workspace/memory, official Eino middlewares, and optional toolsearch.
// MemoryMiddleware.AfterAgent runs here on successful ADK termination (framework applyAfterAgent).
func (r *Runner) BuildADKHandlers(ctx context.Context, chatModel model.ToolCallingChatModel) ([]adk.ChatModelAgentMiddleware, error) {
	stack, err := BuildTypedMiddlewareStack[*schema.Message](ctx, chatModel, r.workspaceRoot, r.dataDir, r.bgCoord)
	if err != nil {
		return nil, err
	}

	var handlers []adk.ChatModelAgentMiddleware
	execution := r.toolExecutionHandlers(ctx)
	handlers = append(handlers, NewModeProfileMiddleware(r.SessionMode()))
	// Armor rollback: no-op unless this run's context carries a rollback spec
	// (armor-break continuation with the rollback_verify_steer strategy).
	handlers = append(handlers, NewArmorRollbackMiddleware())
	handlers = append(handlers, execution.errors)
	if r.paradigm != nil {
		handlers = append(handlers, NewParadigmMiddleware(r.paradigm))
	}
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
			ts, err := toolsearch.New(ctx, toolSearchConfig(ctx, &toolsearch.Config{DynamicTools: dyn}))
			if err != nil {
				return handlers, err
			}
			handlers = append(handlers, ts)
		}
	}
	handlers = append(handlers, execution.calls...)

	// Session spine: final model-context projection. Tool outcome conversion stays outside observers and governance.
	var taskProvider func(context.Context) []string
	if r.adminRunner != nil {
		taskProvider = r.adminRunner.GetActiveTaskGoals
	}
	handlers = append(handlers, NewSessionSpineMiddleware(SessionSpineConfig{
		WorkspaceRoot:      r.workspaceRoot,
		Kernel:             r.SessionKernel(),
		EpisodicCompressor: r.episodicCompressor,
		GetDurableTasks:    taskProvider,
		MaxActiveTurns:     maxActiveTurnsFor(ctx),
		Store:              r.sessStore,
		DataDir:            r.dataDir,
	}))

	return handlers, nil
}
