package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/prebuilt/deep"
	"github.com/cloudwego/eino/compose"

	"agentgo/internal/governance"
)

const adminDeepSessionPrefix = "admin_deep:"

func (r *Runner) runAdminDeep(ctx context.Context, cfg LLMSettings, sessionID, userText string) (string, error) {
	if r.cpStore == nil {
		return "", fmt.Errorf("checkpoint store required for admin deep agent")
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

	deepAgent, err := deep.New(ctx, &deep.Config{
		Name:        "agentgo_admin",
		Description: "Persistent admin task executor with decomposition and re-planning",
		ChatModel:   chatModel,
		Instruction: "你是 AgentGo 管理员智能体。将用户目标拆解为可执行步骤，调用工具完成，并输出最终执行报告。遇到失败时调整计划并重试。",
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               toolList,
				ToolCallMiddlewares: toolMW,
			},
		},
		MaxIteration:           32,
		WithoutGeneralSubAgent: false,
	})
	if err != nil {
		return "", fmt.Errorf("deep.New: %w", err)
	}

	cpID := adminDeepSessionPrefix + sessionID
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           deepAgent,
		CheckPointStore: r.cpStore,
	})
	iter := runner.Query(ctx, userText, adk.WithCheckPointID(cpID))
	content, _, _, err := r.drainADKEvents(ctx, iter, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(content), nil
}
