package governance

import (
	"time"
)

// InterruptKind represents the category of the interrupt.
type InterruptKind string

const (
	KindToolApproval    InterruptKind = "tool_approval"
	KindUserQuestion    InterruptKind = "user_question"
	KindMissingParams   InterruptKind = "missing_params"
	KindOAuthRequired   InterruptKind = "oauth_required"
	KindWorkflowInput   InterruptKind = "workflow_input"
	KindWorkflowConfirm InterruptKind = "workflow_confirm"
	KindSafetyReview    InterruptKind = "safety_review"
	KindCustom          InterruptKind = "custom"
)

// ResumePayload is the structured data returned when an interrupt/approval is resolved.
type ResumePayload struct {
	Approved   bool                   `json:"approved"`
	UserInput  string                 `json:"user_input,omitempty"`
	Arguments  string                 `json:"arguments,omitempty"`
	Params     map[string]interface{} `json:"params,omitempty"`
	OAuthToken string                 `json:"oauth_token,omitempty"`
}

// ApprovalRequest represents a unified approval or interrupt request.
type ApprovalRequest struct {
	ID               string                 `json:"approval_id"`
	Kind             string                 `json:"kind"`
	InterruptKind    InterruptKind          `json:"interrupt_kind"`
	Scope            string                 `json:"scope"`
	Summary          string                 `json:"summary"`
	Prompt           string                 `json:"prompt"`
	Metadata         map[string]interface{} `json:"metadata"`
	Fingerprint      string                 `json:"fingerprint,omitempty"`
	Status           string                 `json:"status"` // pending, approved, rejected
	Approved         *bool                  `json:"approved,omitempty"`
	CreatedAt        int64                  `json:"created_at"`
	ResolvedAt       *int64                 `json:"resolved_at,omitempty"`
	ConsumedAt       *int64                 `json:"consumed_at,omitempty"`
	ResolvedBy       string                 `json:"resolved_by,omitempty"`
	ResolutionNote   string                 `json:"resolution_note,omitempty"`
	Labels           []string               `json:"labels,omitempty"`
	PolicyTags       []string               `json:"policy_tags,omitempty"`
	ResolutionLabels []string               `json:"resolution_labels,omitempty"`
	ResolutionResult interface{}            `json:"resolution_result,omitempty"`
	ResumePayload    *ResumePayload         `json:"resume_payload,omitempty"`
}

func NewApprovalRequest(kind, scope, summary, prompt string) *ApprovalRequest {
	return &ApprovalRequest{
		Kind:          kind,
		InterruptKind: InterruptKind(kind),
		Scope:         scope,
		Summary:       summary,
		Prompt:        prompt,
		Status:        "pending",
		CreatedAt:     time.Now().Unix(),
		Metadata:      make(map[string]interface{}),
	}
}

func (r *ApprovalRequest) RequiresUserInput() bool {
	return r.InterruptKind == KindUserQuestion || r.InterruptKind == KindMissingParams || r.InterruptKind == KindWorkflowInput
}

func (r *ApprovalRequest) RequiresExternalAction() bool {
	return r.InterruptKind == KindOAuthRequired
}
