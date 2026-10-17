package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"agentgo/internal/agent"
	"agentgo/internal/capability"
	"agentgo/internal/checkpoint"
	"agentgo/internal/config"
	"agentgo/internal/governance"
	"agentgo/internal/scheduler"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

// WorkflowRuntime runs workflows: it builds the RunContext (model, tools, sub
// workflows, scheduler, checkpoints), executes or resumes a definition, and
// records the run. It is a snapshot of what the host has wired at the time; the
// host builds a fresh one per call. Nil dependencies degrade (no governance
// without Approvals, no cron without Sched, ...).
type WorkflowRuntime struct {
	Store       *workflow.Store
	Tools       *tools.Registry
	Approvals   *governance.ApprovalQueue
	Runner      *agent.Runner
	Bus         *capability.Bus
	Sched       *scheduler.Store
	Checkpoints *checkpoint.SQLiteStore
	LLM         agent.LLMSettings
	// ChatOnce is the plain model call used when there is no API key or runner.
	ChatOnce func(ctx context.Context, system, user string) (string, error)
	// OnEvent persists/forwards workflow step events.
	OnEvent func(workflow.Event)
}

var errNoWorkflowStore = fmt.Errorf("workflow store unavailable")

func (r *WorkflowRuntime) chatOnce(ctx context.Context, system, user string) (string, error) {
	if r.ChatOnce == nil {
		return "", fmt.Errorf("model unavailable")
	}
	return r.ChatOnce(ctx, system, user)
}

// RunContext builds a mutable RunContext for Eino compose graph tools (shared pointer, per-invoke Input).
func (r *WorkflowRuntime) RunContext(workflowID string) *workflow.RunContext {
	return r.RunContextWithCallbacks(workflowID, nil)
}

func (r *WorkflowRuntime) RunContextWithCallbacks(workflowID string, toolCallbacks []callbacks.Handler) *workflow.RunContext {
	wid := workflowID
	rc := &workflow.RunContext{
		Vars: make(map[string]string),
	}
	cfg := r.LLM
	rc.LLMGenerate = func(ctx context.Context, prompt string) (string, error) {
		if cfg.APIKey == "" || r.Runner == nil {
			return r.chatOnce(ctx, "", prompt)
		}
		res, err := r.Runner.Generate(ctx, cfg, "", prompt, nil)

		if err != nil {
			return "", err
		}
		return res.Content, nil
	}
	rc.ToolRunner = r.ToolRunner(context.Background(), toolCallbacks)
	rc.RunSubworkflow = func(ctx context.Context, subID, subInput string) (string, error) {
		return r.Execute(ctx, subID, subInput)
	}
	rc.AgentRun = func(ctx context.Context, sessionID, prompt string) (string, error) {
		if cfg.APIKey == "" || r.Runner == nil {
			return r.chatOnce(ctx, "", prompt)
		}
		res, err := r.Runner.Generate(ctx, cfg, sessionID, prompt, nil)

		if err != nil {
			return "", err
		}
		return res.Content, nil
	}
	rc.ScheduleCron = func(ctx context.Context, spec, prompt, sessionID string) (string, error) {
		if r.Sched == nil {
			return "", fmt.Errorf("scheduler unavailable")
		}
		job, err := r.Sched.Create("workflow:"+wid, spec, prompt, sessionID)
		if err != nil {
			return "", err
		}
		return job.ID, nil
	}
	rc.LeaseStore = workflow.DefaultLeaseStore()
	if r.Checkpoints != nil && workflowCheckpointEnabled() {
		rc.CheckPointStore = r.Checkpoints
	}
	return rc
}

func (r *WorkflowRuntime) EffectivePolicy() governance.Policy {
	policy := governance.DefaultPolicy()
	if r.Runner != nil {
		policy = r.Runner.EffectivePolicy()
	}
	return policy
}

func (r *WorkflowRuntime) ToolCallbacks() []callbacks.Handler {
	if r.Bus == nil {
		return nil
	}
	return []callbacks.Handler{agent.NewTraceCallbackHandler(r.Bus)}
}

func (r *WorkflowRuntime) Governance() *governance.GovernanceMiddleware {
	if r.Approvals == nil {
		return nil
	}
	return governance.NewGovernanceMiddleware(r.Approvals, r.EffectivePolicy())
}

func (r *WorkflowRuntime) ToolRunner(ctx context.Context, cbs []callbacks.Handler) workflow.ToolRunner {
	if bridge, err := r.ToolsBridge(ctx, cbs); err == nil {
		return bridge
	}
	return workflow.ToolRunnerFunc(func(ctx context.Context, toolName, argsJSON string) (string, error) {
		return r.ToolInvoke(ctx, toolName, argsJSON)
	})
}

func (r *WorkflowRuntime) ToolsBridge(ctx context.Context, cbs []callbacks.Handler) (*workflow.ToolsBridge, error) {
	if r.Tools == nil {
		return nil, fmt.Errorf("tool registry unavailable")
	}
	var toolMW []compose.ToolMiddleware
	if r.Approvals != nil {
		toolMW = append(toolMW, governance.ComposeToolMiddleware(r.Approvals, r.EffectivePolicy()))
	}
	toolMW = agent.AppendComposeToolMiddlewares(toolMW, agent.ComposeToolHealMiddleware())
	reg := r.Tools
	return workflow.NewToolsBridge(ctx, workflow.ToolsBridgeConfig{
		Tools:               reg.GetAllTools(),
		ToolCallMiddlewares: toolMW,
		Callbacks:           cbs,
		Lookup: func(name string) (tool.BaseTool, bool) {
			return reg.Get(name)
		},
	})
}

func (r *WorkflowRuntime) ToolInvoke(ctx context.Context, toolName, argsJSON string) (string, error) {
	if r.Tools == nil {
		return "", fmt.Errorf("tool registry unavailable")
	}
	return tools.GovernedInvokeJSON(ctx, r.Tools, r.Governance(), toolName, argsJSON)
}

func workflowCheckpointEnabled() bool {
	return config.WorkflowCheckpoint.On()
}

func (r *WorkflowRuntime) Execute(ctx context.Context, workflowID, input string) (string, error) {
	if r.Store == nil {
		return "", errNoWorkflowStore
	}
	def, err := r.Store.Get(workflowID)
	if err != nil {
		return "", err
	}
	runID := workflowID + ":" + fmt.Sprintf("%d", time.Now().UnixNano())
	bus := workflow.DefaultSignalBus()

	rc := r.RunContextWithCallbacks(workflowID, r.ToolCallbacks())
	rc.RunID = runID
	rc.Input = input
	rc.CheckPointID = workflow.CheckpointID(workflowID, runID)
	rc.SignalWait = func(ctx context.Context, name string, timeout time.Duration) (string, error) {
		return bus.Wait(ctx, runID, name, timeout)
	}
	rc.SignalEmit = func(ctx context.Context, name, payload string) {
		bus.Emit(runID, name, payload)
	}
	rc.OnEvent = r.OnEvent

	if r.Store != nil {
		_ = r.Store.SaveRun(workflow.RunRecord{
			ID:         runID,
			WorkflowID: workflowID,
			Input:      input,
			Status:     "running",
		})
	}

	out, err := workflow.Execute(ctx, def, *rc)

	if r.Store != nil {
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
		_ = r.Store.SaveRun(workflow.RunRecord{
			ID:         runID,
			WorkflowID: workflowID,
			Input:      input,
			Output:     out,
			Status:     status,
			Error:      errStr,
		})
	}

	if err == nil && r.Bus != nil {
		r.Bus.PublishWorkflowDone(agent.SessionIDFromContext(ctx), workflowID, out)
	}
	return out, err
}

func (r *WorkflowRuntime) Resume(ctx context.Context, workflowID, checkPointID, interruptID, resumeJSON string) (string, error) {
	if r.Store == nil {
		return "", errNoWorkflowStore
	}
	def, err := r.Store.Get(workflowID)
	if err != nil {
		return "", err
	}
	runID := checkPointID
	if prefix := "wf:" + workflowID + ":"; strings.HasPrefix(checkPointID, prefix) {
		runID = strings.TrimPrefix(checkPointID, prefix)
	}
	bus := workflow.DefaultSignalBus()
	rc := r.RunContextWithCallbacks(workflowID, r.ToolCallbacks())
	rc.RunID = runID
	rc.CheckPointID = strings.TrimSpace(checkPointID)
	rc.SignalWait = func(ctx context.Context, name string, timeout time.Duration) (string, error) {
		return bus.Wait(ctx, runID, name, timeout)
	}
	rc.SignalEmit = func(ctx context.Context, name, payload string) {
		bus.Emit(runID, name, payload)
	}
	rc.OnEvent = r.OnEvent

	if r.Store != nil {
		if runRec, getErr := r.Store.GetRun(runID); getErr == nil {
			runRec.Status = "running"
			_ = r.Store.SaveRun(runRec)
		} else {
			_ = r.Store.SaveRun(workflow.RunRecord{
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

	if r.Store != nil {
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
		if runRec, getErr := r.Store.GetRun(runID); getErr == nil {
			runRec.Status = status
			runRec.Output = out
			runRec.Error = errStr
			runRec.UpdatedAt = time.Now().Unix()
			_ = r.Store.SaveRun(runRec)
		} else {
			_ = r.Store.SaveRun(workflow.RunRecord{
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
