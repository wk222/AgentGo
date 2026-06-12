package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"agentgo/internal/agent"
	"agentgo/internal/governance"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

// workflowRunContextPtr builds a mutable RunContext for Eino compose graph tools (shared pointer, per-invoke Input).
func (r *Runtime) workflowRunContextPtr(workflowID string) *workflow.RunContext {
	return r.workflowRunContextPtrWithCallbacks(workflowID, nil)
}

func (r *Runtime) workflowRunContextPtrWithCallbacks(workflowID string, toolCallbacks []callbacks.Handler) *workflow.RunContext {
	wid := workflowID
	rc := &workflow.RunContext{
		Vars: make(map[string]string),
	}
	cfg := r.LLMConfig()
	rc.LLMGenerate = func(ctx context.Context, prompt string) (string, error) {
		if cfg.APIKey == "" {
			return ChatOnce(ctx, cfg, "", prompt)
		}
		res, err := r.agentRunner.Generate(ctx, agent.LLMSettings{
			APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.Model, FallbackModel: cfg.FallbackModel,
		}, "", prompt, nil)

		if err != nil {
			return "", err
		}
		return res.Content, nil
	}
	rc.ToolRunner = r.buildWorkflowToolRunner(context.Background(), toolCallbacks)
	rc.RunSubworkflow = func(ctx context.Context, subID, subInput string) (string, error) {
		return r.executeWorkflow(ctx, subID, subInput)
	}
	rc.AgentRun = func(ctx context.Context, sessionID, prompt string) (string, error) {
		if cfg.APIKey == "" {
			return ChatOnce(ctx, cfg, "", prompt)
		}
		res, err := r.agentRunner.Generate(ctx, agent.LLMSettings{
			APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.Model, FallbackModel: cfg.FallbackModel,
		}, sessionID, prompt, nil)

		if err != nil {
			return "", err
		}
		return res.Content, nil
	}
	rc.ScheduleCron = func(ctx context.Context, spec, prompt, sessionID string) (string, error) {
		if r.sched == nil {
			return "", fmt.Errorf("scheduler unavailable")
		}
		job, err := r.sched.Create("workflow:"+wid, spec, prompt, sessionID)
		if err != nil {
			return "", err
		}
		return job.ID, nil
	}
	rc.LeaseStore = workflow.DefaultLeaseStore()
	if r.cpStore != nil && workflowCheckpointEnabled() {
		rc.CheckPointStore = r.cpStore
	}
	return rc
}

func (r *Runtime) workflowEffectivePolicy() governance.Policy {
	policy := governance.DefaultPolicy()
	if r.agentRunner != nil {
		policy = r.agentRunner.EffectivePolicy()
	}
	return policy
}

func (r *Runtime) workflowToolCallbacks() []callbacks.Handler {
	if r.capBus == nil {
		return nil
	}
	return []callbacks.Handler{agent.NewTraceCallbackHandler(r.capBus)}
}

func (r *Runtime) workflowGovernance() *governance.GovernanceMiddleware {
	if r.approvals == nil {
		return nil
	}
	return governance.NewGovernanceMiddleware(r.approvals, r.workflowEffectivePolicy())
}

func (r *Runtime) buildWorkflowToolRunner(ctx context.Context, cbs []callbacks.Handler) workflow.ToolRunner {
	if bridge, err := r.buildWorkflowToolsBridge(ctx, cbs); err == nil {
		return bridge
	}
	return workflow.ToolRunnerFunc(func(ctx context.Context, toolName, argsJSON string) (string, error) {
		return r.workflowToolInvoke(ctx, toolName, argsJSON)
	})
}

func (r *Runtime) buildWorkflowToolsBridge(ctx context.Context, cbs []callbacks.Handler) (*workflow.ToolsBridge, error) {
	if r.toolReg == nil {
		return nil, fmt.Errorf("tool registry unavailable")
	}
	var toolMW []compose.ToolMiddleware
	if r.approvals != nil {
		toolMW = append(toolMW, governance.ComposeToolMiddleware(r.approvals, r.workflowEffectivePolicy()))
	}
	toolMW = agent.AppendComposeToolMiddlewares(toolMW, agent.ComposeToolHealMiddleware())
	reg := r.toolReg
	return workflow.NewToolsBridge(ctx, workflow.ToolsBridgeConfig{
		Tools:               reg.GetAllTools(),
		ToolCallMiddlewares: toolMW,
		Callbacks:           cbs,
		Lookup: func(name string) (tool.BaseTool, bool) {
			return reg.Get(name)
		},
	})
}

func (r *Runtime) workflowToolInvoke(ctx context.Context, toolName, argsJSON string) (string, error) {
	if r.toolReg == nil {
		return "", fmt.Errorf("tool registry unavailable")
	}
	return tools.GovernedInvokeJSON(ctx, r.toolReg, r.workflowGovernance(), toolName, argsJSON)
}

func workflowCheckpointEnabled() bool {
	v := strings.TrimSpace(os.Getenv("AGENTGO_WORKFLOW_CHECKPOINT"))
	if v == "0" || strings.EqualFold(v, "false") {
		return false
	}
	return true
}

func (r *Runtime) executeWorkflow(ctx context.Context, workflowID, input string) (string, error) {
	def, err := r.wfStore.Get(workflowID)
	if err != nil {
		return "", err
	}
	runID := workflowID + ":" + fmt.Sprintf("%d", time.Now().UnixNano())
	bus := workflow.DefaultSignalBus()

	rc := r.workflowRunContextPtrWithCallbacks(workflowID, r.workflowToolCallbacks())
	rc.RunID = runID
	rc.Input = input
	rc.CheckPointID = workflow.CheckpointID(workflowID, runID)
	rc.SignalWait = func(ctx context.Context, name string, timeout time.Duration) (string, error) {
		return bus.Wait(ctx, runID, name, timeout)
	}
	rc.SignalEmit = func(ctx context.Context, name, payload string) {
		bus.Emit(runID, name, payload)
	}
	rc.OnEvent = r.recordWorkflowEvent

	if r.wfStore != nil {
		_ = r.wfStore.SaveRun(workflow.RunRecord{
			ID:         runID,
			WorkflowID: workflowID,
			Input:      input,
			Status:     "running",
		})
	}

	out, err := workflow.Execute(ctx, def, *rc)

	if r.wfStore != nil {
		status := "completed"
		var errStr string
		if err != nil {
			if _, ok := workflow.AsInterrupt(err); ok {
				status = "paused"
			} else {
				status = "failed"
			}
			errStr = err.Error()
		}
		_ = r.wfStore.SaveRun(workflow.RunRecord{
			ID:         runID,
			WorkflowID: workflowID,
			Input:      input,
			Output:     out,
			Status:     status,
			Error:      errStr,
		})
	}

	if err == nil && r.capBus != nil {
		r.capBus.PublishWorkflowDone(agent.SessionIDFromContext(ctx), workflowID, out)
	}
	return out, err
}

func (r *Runtime) resumeWorkflow(ctx context.Context, workflowID, checkPointID, interruptID, resumeJSON string) (string, error) {
	def, err := r.wfStore.Get(workflowID)
	if err != nil {
		return "", err
	}
	runID := checkPointID
	if prefix := "wf:" + workflowID + ":"; strings.HasPrefix(checkPointID, prefix) {
		runID = strings.TrimPrefix(checkPointID, prefix)
	}
	bus := workflow.DefaultSignalBus()
	rc := r.workflowRunContextPtrWithCallbacks(workflowID, r.workflowToolCallbacks())
	rc.RunID = runID
	rc.CheckPointID = strings.TrimSpace(checkPointID)
	rc.SignalWait = func(ctx context.Context, name string, timeout time.Duration) (string, error) {
		return bus.Wait(ctx, runID, name, timeout)
	}
	rc.SignalEmit = func(ctx context.Context, name, payload string) {
		bus.Emit(runID, name, payload)
	}
	rc.OnEvent = r.recordWorkflowEvent

	if r.wfStore != nil {
		if runRec, getErr := r.wfStore.GetRun(runID); getErr == nil {
			runRec.Status = "running"
			_ = r.wfStore.SaveRun(runRec)
		} else {
			_ = r.wfStore.SaveRun(workflow.RunRecord{
				ID:         runID,
				WorkflowID: workflowID,
				Status:     "running",
			})
		}
	}

	var resumeData any
	if strings.TrimSpace(resumeJSON) != "" {
		var probe struct {
			Approved *bool `json:"approved"`
		}
		if err := json.Unmarshal([]byte(resumeJSON), &probe); err == nil && probe.Approved != nil {
			var approvalResume governance.ResumePayload
			if err := json.Unmarshal([]byte(resumeJSON), &approvalResume); err == nil {
				resumeData = approvalResume
			}
		}
		if resumeData == nil {
			_ = json.Unmarshal([]byte(resumeJSON), &resumeData)
		}
	}

	out, err := workflow.ResumeExecute(ctx, def, *rc, interruptID, resumeData)

	if r.wfStore != nil {
		status := "completed"
		var errStr string
		if err != nil {
			if _, ok := workflow.AsInterrupt(err); ok {
				status = "paused"
			} else {
				status = "failed"
			}
			errStr = err.Error()
		}
		if runRec, getErr := r.wfStore.GetRun(runID); getErr == nil {
			runRec.Status = status
			runRec.Output = out
			runRec.Error = errStr
			runRec.UpdatedAt = time.Now().Unix()
			_ = r.wfStore.SaveRun(runRec)
		} else {
			_ = r.wfStore.SaveRun(workflow.RunRecord{
				ID:         runID,
				WorkflowID: workflowID,
				Status:     status,
				Output:     out,
				Error:      errStr,
			})
		}
	}

	return out, err
}
