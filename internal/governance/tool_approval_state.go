package governance

import "github.com/cloudwego/eino/schema"

func init() {
	schema.RegisterName[ToolApprovalPause]("_agentgo_governance_tool_approval_pause")
}

// ToolApprovalPause is persisted across ADK StatefulInterrupt resume.
type ToolApprovalPause struct {
	ApprovalID string
	ToolName   string
	Arguments  string
}
