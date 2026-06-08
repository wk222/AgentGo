package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"agentgo/internal/governance"
)

func (r *Runner) buildChatModelAgentWithTools(
	ctx context.Context,
	cfg LLMSettings,
	name, description, instruction string,
	toolList []tool.BaseTool,
	maxIter int,
	emitInternal bool,
) (*adk.ChatModelAgent, error) {
	chatModel, err := r.newOpenAIModel(ctx, cfg, cfg.Model)
	if err != nil {
		return nil, err
	}
	var toolMW []compose.ToolMiddleware
	policy := r.effectivePolicy()
	if r.queue != nil {
		toolMW = append(toolMW, governance.ComposeToolMiddleware(r.queue, policy))
	}
	toolMW = append(toolMW, ComposeToolHealMiddleware())
	toolsCfg := adk.ToolsConfig{
		EmitInternalEvents: emitInternal,
		ToolsNodeConfig: compose.ToolsNodeConfig{
			Tools:               toolList,
			ToolCallMiddlewares: toolMW,
		},
	}
	failover, err := r.modelFailoverConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if maxIter <= 0 {
		maxIter = 12
	}
	var handlers []adk.ChatModelAgentMiddleware
	if r.queue != nil {
		handlers = append(handlers, governance.NewGovernanceMiddleware(r.queue, policy))
	}
	agentCfg := &adk.ChatModelAgentConfig{
		Name:                name,
		Description:         description,
		Instruction:         instruction,
		Model:               chatModel,
		ToolsConfig:         toolsCfg,
		Handlers:            handlers,
		MaxIterations:       maxIter,
		ModelRetryConfig:    DefaultModelRetryConfig(),
		ModelFailoverConfig: failover,
	}
	return adk.NewChatModelAgent(ctx, agentCfg)
}

func (r *Runner) workflowSpecialistTools() []tool.BaseTool {
	if r.toolReg == nil {
		return nil
	}
	allow := map[string]bool{"run_workflow": true}
	var out []tool.BaseTool
	for _, t := range r.toolReg.GetAllTools() {
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		name := info.Name
		if allow[name] || strings.HasPrefix(name, "workflow_") {
			out = append(out, t)
		}
	}
	return out
}

func (r *Runner) innerAppSpecialistTools() []tool.BaseTool {
	if r.toolReg == nil {
		return nil
	}
	allow := map[string]bool{
		"invoke_inner_app": true, "list_inner_apps": true, "invoke_app_capability": true,
	}
	var out []tool.BaseTool
	for _, t := range r.toolReg.GetAllTools() {
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		if allow[info.Name] {
			out = append(out, t)
		}
	}
	return out
}

func (r *Runner) buildWorkflowSpecialistAgent(ctx context.Context, cfg LLMSettings) (adk.Agent, error) {
	toolList := r.workflowSpecialistTools()
	if len(toolList) == 0 {
		return nil, fmt.Errorf("no workflow tools registered")
	}
	return r.buildChatModelAgentWithTools(ctx, cfg,
		"workflow_agent",
		"执行已保存的 Eino Compose 工作流（run_workflow 或 workflow_* 工具）",
		`你是工作流专家。收到任务后：
1. 选择合适的工作流 ID/名称（workflow_* 或 run_workflow）
2. 传入清晰 input
3. 返回执行结果摘要，不要编造未执行的输出`,
		toolList, 10, MatrixEmitInternalEvents(),
	)
}

func (r *Runner) buildInnerAppSpecialistAgent(ctx context.Context, cfg LLMSettings) (adk.Agent, error) {
	toolList := r.innerAppSpecialistTools()
	if len(toolList) == 0 {
		return nil, fmt.Errorf("no inner app tools registered")
	}
	return r.buildChatModelAgentWithTools(ctx, cfg,
		"inner_app_agent",
		"调用系统内置应用（invoke_inner_app / invoke_app_capability）",
		`你是 Inner App 专家。收到任务后：
1. 可用 list_inner_apps 查看应用
2. 用 invoke_inner_app 或 invoke_app_capability 执行
3. 返回结构化结果摘要`,
		toolList, 10, MatrixEmitInternalEvents(),
	)
}

// BuildMatrixCoordinatorAgent wraps workflow_agent and inner_app_agent as AgentTools (Eino 推荐协作模式).
func (r *Runner) BuildMatrixCoordinatorAgent(ctx context.Context, cfg LLMSettings) (adk.Agent, error) {
	wfAgent, err := r.buildWorkflowSpecialistAgent(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("workflow_agent: %w", err)
	}
	appAgent, err := r.buildInnerAppSpecialistAgent(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("inner_app_agent: %w", err)
	}
	wfTool := adk.NewAgentTool(ctx, wfAgent)
	appTool := adk.NewAgentTool(ctx, appAgent)

	coordinatorTools := []tool.BaseTool{wfTool, appTool}
	if r.toolReg != nil {
		for _, t := range r.toolReg.GetStaticToolsForMode(map[string]bool{"get_current_time": true}) {
			coordinatorTools = append(coordinatorTools, t)
		}
	}

	return r.buildChatModelAgentWithTools(ctx, cfg,
		"app_matrix_coordinator",
		"App Matrix 总协调者：委派工作流或内置应用子 Agent 完成任务",
		`你是 App Matrix 总协调者（L5 编排）。收到能力总线事件后：
- 需要跑工作流：调用 workflow_agent，在 request 中写清 workflow_id 与 input
- 需要调内置应用：调用 inner_app_agent
- 可并行委派；完成后用中文总结下一步建议
不要亲自编造工具执行结果。`,
		coordinatorTools, 16, MatrixEmitInternalEvents(),
	)
}

// RunMatrixSupervisor runs the AgentTool-based coordinator (Eino agent_collaboration 推荐路径).
func (r *Runner) RunMatrixSupervisor(ctx context.Context, cfg LLMSettings, sessionID, userText string) (*RunResult, error) {
	if r.cpStore == nil {
		return nil, fmt.Errorf("checkpoint store unavailable")
	}
	ctx = WithSessionMode(WithSessionID(ctx, sessionID), SessionMode{
		Profile: ModeAppMatrix, Canvas: CanvasDeep,
	})

	coordinator, err := r.BuildMatrixCoordinatorAgent(ctx, cfg)
	if err != nil {
		return nil, err
	}

	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           coordinator,
		CheckPointStore: r.cpStore,
		EnableStreaming: false,
	})

	opts := r.queryOptions(ctx, sessionID)
	if r.runControl != nil {
		defer r.runControl.Clear(sessionID)
	}

	iter := runner.Query(ctx, userText, opts...)
	content, pause, interruptID, err := r.drainADKEvents(ctx, iter, nil)
	if err != nil {
		return nil, err
	}
	if pause != nil && interruptID != "" {
		if pause.InterruptID == "" {
			pause.InterruptID = interruptID
		}
		if pause.ApprovalID == "" {
			pause.ApprovalID = interruptID
		}
		return &RunResult{Content: content, PendingApproval: pause, UsedTools: true}, nil
	}
	return &RunResult{Content: content, UsedTools: true}, nil
}

// ResumeMatrixSupervisor continues a paused App Matrix coordinator run after HITL approval.
func (r *Runner) ResumeMatrixSupervisor(ctx context.Context, cfg LLMSettings, interruptID string, resumeData any) (*RunResult, error) {
	if r.cpStore == nil {
		return nil, fmt.Errorf("checkpoint store unavailable")
	}
	ctx = WithSessionMode(WithSessionID(ctx, MatrixCoordinatorSession), SessionMode{
		Profile: ModeAppMatrix, Canvas: CanvasDeep,
	})
	coordinator, err := r.BuildMatrixCoordinatorAgent(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return r.resumeADKAgent(ctx, cfg, MatrixCoordinatorSession, interruptID, resumeData, coordinator, nil)
}

// MatrixEventPrompt formats a capability bus event for the coordinator.
func MatrixEventPrompt(evType, source, sessionID string, payload map[string]string) string {
	var b strings.Builder
	b.WriteString("[App Matrix 能力总线事件]\n")
	b.WriteString("type: ")
	b.WriteString(evType)
	b.WriteString("\nsource: ")
	b.WriteString(source)
	if sessionID != "" {
		b.WriteString("\nsession: ")
		b.WriteString(sessionID)
	}
	for k, v := range payload {
		if v == "" {
			continue
		}
		line := v
		if len(line) > 6000 {
			line = line[:6000] + "\n…(truncated)"
		}
		b.WriteString("\n")
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(line)
	}
	b.WriteString("\n\n请委派子 Agent 处理并总结。")
	return b.String()
}
