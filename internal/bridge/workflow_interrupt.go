package bridge

import (
	"context"
	"encoding/json"

	"agentgo/internal/governance"
	"agentgo/internal/workflow"
)

// workflowInterruptResponse builds the IPC map for a paused workflow run.
func (r *Runtime) workflowInterruptResponse(workflowID string, ie *workflow.InterruptError) map[string]any {
	meta := workflow.MetaFromInterrupt(ie)
	out := map[string]any{
		"success":        false,
		"interrupted":    true,
		"interrupt_kind": string(meta.Kind),
		"checkpoint_id":  ie.CheckPointID,
		"interrupt_id":   meta.InterruptID,
		"error":          ie.Error(),
	}
	if meta.Kind == workflow.InterruptToolApproval {
		out["approval_id"] = meta.ApprovalID
		out["tool_name"] = meta.ToolName
		out["arguments"] = meta.Arguments
		if r != nil && r.pending != nil {
			approvalID := meta.ApprovalID
			if approvalID == "" {
				approvalID = meta.InterruptID
			}
			r.pending.Set(pendingRun{
				ResumeKind:   "workflow",
				WorkflowID:   workflowID,
				CheckPointID: ie.CheckPointID,
				InterruptID:  meta.InterruptID,
				ApprovalID:   approvalID,
				ToolName:     meta.ToolName,
				Arguments:    meta.Arguments,
			})
		}
	}
	return out
}

func (r *Runtime) resumeWorkflowAfterApproval(ctx context.Context, pr pendingRun, resume *governance.ResumePayload) (string, error) {
	if r == nil || r.wfResume == nil {
		return "", errWorkflowResumeUnavailable
	}
	if resume == nil {
		resume = &governance.ResumePayload{}
	}
	b, err := json.Marshal(resume)
	if err != nil {
		return "", err
	}
	return r.wfResume(ctx, pr.WorkflowID, pr.CheckPointID, pr.InterruptID, string(b))
}

var errWorkflowResumeUnavailable = errBridge("workflow resume unavailable")

type errBridge string

func (e errBridge) Error() string { return string(e) }
