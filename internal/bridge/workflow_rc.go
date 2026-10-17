package bridge

import (
	"context"

	"github.com/cloudwego/eino/callbacks"

	"agentgo/internal/agent"
	"agentgo/internal/core"
	"agentgo/internal/workflow"
)

// Workflow execution lives in core (core/workflow_runtime.go). Each call takes a
// snapshot of what this process has wired, so tests and hot reloads that swap
// the runner, tools or stores are seen immediately.

func (r *Runtime) workflowRuntime() *core.WorkflowRuntime {
	cfg := r.LLMConfig()
	return &core.WorkflowRuntime{
		Store: r.wfStore, Tools: r.toolReg, Approvals: r.approvals, Runner: r.agentRunner,
		Bus: r.capBus, Sched: r.sched, Checkpoints: r.cpStore,
		LLM: agent.LLMSettings{APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.Model, FallbackModel: cfg.FallbackModel},
		ChatOnce: func(ctx context.Context, system, user string) (string, error) {
			return ChatOnce(ctx, cfg, system, user)
		},
		OnEvent: r.recordWorkflowEvent,
	}
}

// workflowRunContextPtr builds a mutable RunContext for Eino compose graph tools.
func (r *Runtime) workflowRunContextPtr(workflowID string) *workflow.RunContext {
	return r.workflowRuntime().RunContext(workflowID)
}

func (r *Runtime) buildWorkflowToolsBridge(ctx context.Context, cbs []callbacks.Handler) (*workflow.ToolsBridge, error) {
	return r.workflowRuntime().ToolsBridge(ctx, cbs)
}

func (r *Runtime) workflowToolInvoke(ctx context.Context, toolName, argsJSON string) (string, error) {
	return r.workflowRuntime().ToolInvoke(ctx, toolName, argsJSON)
}

func (r *Runtime) executeWorkflow(ctx context.Context, workflowID, input string) (string, error) {
	return r.workflowRuntime().Execute(ctx, workflowID, input)
}

func (r *Runtime) resumeWorkflow(ctx context.Context, workflowID, checkPointID, interruptID, resumeJSON string) (string, error) {
	return r.workflowRuntime().Resume(ctx, workflowID, checkPointID, interruptID, resumeJSON)
}
