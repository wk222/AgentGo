package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/compose"
)

// AgentNodeExecutor runs a full ADK/agent turn (PyFlow agent node).
type AgentNodeExecutor struct{}

func (e *AgentNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	prompt := n.Prompt
	if prompt == "" {
		prompt = last
	}
	prompt = expandTemplateVars(prompt, input, last, rc.Vars)
	sid := "workflow:" + rc.RunID
	if n.Config != nil {
		if s, ok := n.Config["session_id"].(string); ok && strings.TrimSpace(s) != "" {
			sid = s
		}
	}
	if rc.AgentRun != nil {
		return rc.AgentRun(ctx, sid, prompt)
	}
	if rc.LLMGenerate != nil {
		return rc.LLMGenerate(ctx, prompt)
	}
	return "", fmt.Errorf("agent node %s: no agent runner", n.ID)
}

// WaitSignalNodeExecutor blocks until a named signal arrives.
type WaitSignalNodeExecutor struct{}

func (e *WaitSignalNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	name := n.ToolName
	if name == "" && n.Config != nil {
		if s, ok := n.Config["signal"].(string); ok {
			name = s
		}
		if s, ok := n.Config["name"].(string); ok && name == "" {
			name = s
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("wait_signal node %s: signal name required", n.ID)
	}
	timeout := 5 * time.Minute
	if n.Config != nil {
		switch v := n.Config["timeout_ms"].(type) {
		case float64:
			timeout = time.Duration(v) * time.Millisecond
		case int:
			timeout = time.Duration(v) * time.Millisecond
		}
	}
	// Eino checkpoint path: pause with StatefulInterrupt, resume via ResumeWorkflow + payload.
	if rc.CheckPointStore != nil {
		wasInterrupted, hasState, _ := compose.GetInterruptState[waitSignalState](ctx)
		if wasInterrupted && hasState {
			if isResume, hasData, rd := compose.GetResumeContext[waitResumeData](ctx); isResume && hasData && rd.Payload != "" {
				return rd.Payload, nil
			}
			if isResume, hasData, m := compose.GetResumeContext[map[string]any](ctx); isResume && hasData {
				if p, ok := m["payload"].(string); ok && p != "" {
					return p, nil
				}
			}
		}
		return "", compose.StatefulInterrupt(ctx,
			map[string]any{"signal": name, "run_id": rc.RunID},
			waitSignalState{Signal: name, RunID: rc.RunID},
		)
	}
	if rc.SignalWait != nil {
		return rc.SignalWait(ctx, name, timeout)
	}
	bus := DefaultSignalBus()
	return bus.Wait(ctx, rc.RunID, name, timeout)
}

// EmitSignalNodeExecutor publishes a workflow signal.
type EmitSignalNodeExecutor struct{}

func (e *EmitSignalNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	name := n.ToolName
	payload := last
	if n.Config != nil {
		if s, ok := n.Config["signal"].(string); ok {
			name = s
		}
		if p, ok := n.Config["payload"].(string); ok && p != "" {
			payload = expandTemplateVars(p, input, last, rc.Vars)
		}
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("emit_signal node %s: signal name required", n.ID)
	}
	if rc.SignalEmit != nil {
		rc.SignalEmit(ctx, name, payload)
	} else {
		DefaultSignalBus().Emit(rc.RunID, name, payload)
	}
	return payload, nil
}
