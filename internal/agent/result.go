package agent

import "agentgo/internal/interactive"

// RunResult is the outcome of a ReAct turn (sync or stream).
type RunResult struct {
	Content         string
	PendingApproval *PendingApproval
	UsedTools       bool
}

// PendingApproval mirrors governance.RunPause / tool.Interrupt for bridge DTO mapping.
type PendingApproval struct {
	ApprovalID  string // governance queue id when tool approval
	InterruptID string // Eino checkpoint interrupt id for ResumeWithParams
	ToolName    string
	Arguments   string
	Question    *interactive.QuestionPayload
}