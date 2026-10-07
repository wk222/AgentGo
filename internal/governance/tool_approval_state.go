package governance

import (
	"context"
	"encoding/gob"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func init() {
	schema.RegisterName[ToolApprovalPause]("_agentgo_governance_tool_approval_pause")
	// The interrupt info handed to tool.StatefulInterrupt is a map[string]any
	// (see GovernanceMiddleware.interruptInfo). It is stored in the ADK
	// checkpoint as an interface value, and gob refuses unregistered concrete
	// types ("type not registered for interface: map[string]interface {}"),
	// which made every approval-requiring tool call fail the whole run.
	gob.Register(map[string]any{})
	gob.Register([]any{})
}

// resumePayloadFrom reads the approval decision handed to ResumeWithParams.
// Callers pass either a ResumePayload or a *ResumePayload (the bridge builds
// pointers because the same value also goes to ApprovalQueue.Resolve), but
// eino's GetResumeContext[T] is an exact type assertion: asking only for the
// value type silently misses a pointer, the tool then re-interrupts at once,
// and an approved call never runs.
func resumePayloadFrom(ctx context.Context) (isResume, hasData bool, data ResumePayload) {
	isResume, hasData, data = tool.GetResumeContext[ResumePayload](ctx)
	if hasData {
		return
	}
	if isPtr, hasPtr, p := tool.GetResumeContext[*ResumePayload](ctx); hasPtr && p != nil {
		return isPtr, true, *p
	}
	return
}

// ToolApprovalPause is persisted across ADK StatefulInterrupt resume.
type ToolApprovalPause struct {
	ApprovalID string
	ToolName   string
	Arguments  string
}
