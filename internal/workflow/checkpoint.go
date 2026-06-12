package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

func init() {
	schema.RegisterName[*WorkflowState]("_agentgo_workflow_state")
	schema.RegisterName[waitSignalState]("_agentgo_wait_signal_state")
	schema.RegisterName[waitResumeData]("_agentgo_wait_resume_data")
	schema.RegisterName[askUserState]("_agentgo_ask_user_state")
}

// waitSignalState is persisted across wait_signal compose interrupts.
type waitSignalState struct {
	Signal string
	RunID  string
}

// waitResumeData is supplied via compose.ResumeWithData for wait_signal nodes.
type waitResumeData struct {
	Payload string `json:"payload"`
}

// askUserState is persisted for PyFlow ask_user compose interrupts.
type askUserState struct {
	Prompt string `json:"prompt"`
}

// CheckpointID returns a stable compose checkpoint key for a workflow run.
func CheckpointID(workflowID, runID string) string {
	wid := strings.TrimSpace(workflowID)
	if wid == "" {
		wid = "unnamed"
	}
	rid := strings.TrimSpace(runID)
	if rid == "" {
		rid = "default"
	}
	return fmt.Sprintf("wf:%s:%s", wid, rid)
}

func workflowGraphName(def Definition) string {
	name := strings.TrimSpace(def.Name)
	if name == "" {
		name = strings.TrimSpace(def.ID)
	}
	if name == "" {
		name = "unnamed"
	}
	return "agentgo_workflow:" + sanitizeToolName(name)
}

func invokeCheckpointOptions(rc *RunContext, workflowID string) []compose.Option {
	if rc == nil || rc.CheckPointStore == nil {
		return nil
	}
	cpID := strings.TrimSpace(rc.CheckPointID)
	if cpID == "" {
		cpID = CheckpointID(workflowID, rc.RunID)
	}
	return []compose.Option{compose.WithCheckPointID(cpID)}
}

// InterruptError is returned when a compose graph stops on Eino interrupt (HITL / signal / branch pause).
type InterruptError struct {
	CheckPointID string
	InterruptID  string
	Info         *compose.InterruptInfo
}

func (e *InterruptError) Error() string {
	if e == nil {
		return "workflow interrupted"
	}
	return fmt.Sprintf("workflow interrupted (checkpoint=%s interrupt=%s)", e.CheckPointID, e.InterruptID)
}

// AsInterrupt unwraps a workflow interrupt for bridge / IPC resume.
func AsInterrupt(err error) (*InterruptError, bool) {
	var ie *InterruptError
	if errors.As(err, &ie) {
		return ie, true
	}
	return nil, false
}

func wrapInterrupt(workflowID string, rc *RunContext, err error) error {
	info, ok := compose.ExtractInterruptInfo(err)
	if !ok {
		return err
	}
	cpID := ""
	if rc != nil {
		cpID = strings.TrimSpace(rc.CheckPointID)
		if cpID == "" {
			cpID = CheckpointID(workflowID, rc.RunID)
		}
	}
	iid := ""
	if ic := pickRootInterruptCtx(info.InterruptContexts); ic != nil {
		iid = ic.ID
	} else if len(info.InterruptContexts) > 0 && info.InterruptContexts[0] != nil {
		iid = info.InterruptContexts[0].ID
	}
	return &InterruptError{CheckPointID: cpID, InterruptID: iid, Info: info}
}

// ResumeExecute continues a checkpointed workflow after compose.ResumeWithData.
func ResumeExecute(ctx context.Context, def Definition, rc RunContext, interruptID string, resumeData any) (string, error) {
	if rc.CheckPointStore == nil {
		return "", fmt.Errorf("checkpoint store required for resume")
	}
	if strings.TrimSpace(interruptID) == "" {
		return "", fmt.Errorf("interrupt id required")
	}
	if rc.OnEvent == nil {
		rc.OnEvent = func(e Event) {}
	}
	if rc.Vars == nil {
		rc.Vars = make(map[string]string)
	}
	rcPtr := &rc
	runnable, err := CompileToCompose(ctx, def, rcPtr)
	if err != nil {
		return "", err
	}
	wid := strings.TrimSpace(def.ID)
	if wid == "" {
		wid = def.Name
	}
	initial := &WorkflowState{OriginalInput: rc.Input, Vars: rc.Vars}
	opts := invokeCheckpointOptions(rcPtr, wid)
	rCtx := compose.ResumeWithData(ctx, interruptID, resumeData)
	out, err := runnable.Invoke(rCtx, initial, opts...)
	if err != nil {
		return "", wrapInterrupt(wid, rcPtr, err)
	}
	if out != nil {
		return out.LastOutput, nil
	}
	return "", nil
}
