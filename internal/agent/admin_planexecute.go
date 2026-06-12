package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/planexecute"
	"github.com/cloudwego/eino/compose"

	"agentgo/internal/checkpoint"
	"agentgo/internal/governance"
)

const adminPlanExecuteSessionPrefix = "admin_pe:"

// runAdminPlanExecute runs admin goals via Eino planexecute (plan → execute → replan).
func (r *Runner) runAdminPlanExecute(ctx context.Context, cfg LLMSettings, sessionID, userText string) (string, error) {
	if r.cpStore == nil {
		return "", fmt.Errorf("checkpoint store required for admin planexecute")
	}
	ctx = WithSessionMode(ctx, SessionMode{Profile: ModeAdmin, Canvas: CanvasDeep})
	ctx = BindRunContext(ctx, sessionID)

	chatModel, err := r.newOpenAIModel(ctx, cfg, cfg.Model)
	if err != nil {
		return "", err
	}

	sm := SessionModeFromContext(ctx)
	toolList := RegistryToolsForMode(r.toolReg, sm)
	var toolMW []compose.ToolMiddleware
	policy := r.effectivePolicy()
	if r.queue != nil {
		toolMW = append(toolMW, governance.ComposeToolMiddleware(r.queue, policy))
	}
	toolMW = append(toolMW, ComposeToolHealMiddleware())

	planner, err := planexecute.NewPlanner(ctx, &planexecute.PlannerConfig{
		ToolCallingChatModel: chatModel,
	})
	if err != nil {
		return "", fmt.Errorf("planexecute planner: %w", err)
	}

	executor, err := planexecute.NewExecutor(ctx, &planexecute.ExecutorConfig{
		Model: chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               toolList,
				ToolCallMiddlewares: toolMW,
			},
		},
		MaxIterations: 16,
	})
	if err != nil {
		return "", fmt.Errorf("planexecute executor: %w", err)
	}

	replanner, err := planexecute.NewReplanner(ctx, &planexecute.ReplannerConfig{
		ChatModel: chatModel,
	})
	if err != nil {
		return "", fmt.Errorf("planexecute replanner: %w", err)
	}

	peAgent, err := planexecute.New(ctx, &planexecute.Config{
		Planner:       planner,
		Executor:      executor,
		Replanner:     replanner,
		MaxIterations: 6,
	})
	if err != nil {
		return "", fmt.Errorf("planexecute.New: %w", err)
	}

	cpID := adminPlanExecuteSessionPrefix + checkpoint.CheckpointIDForSession(sessionID)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           peAgent,
		CheckPointStore: r.cpStore,
	})
	iter := runner.Query(ctx, userText, adk.WithCheckPointID(cpID))
	content, _, _, err := r.drainADKEvents(ctx, iter, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(content), nil
}
