package governance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

// RiskLevel represents the 4-tier risk classification from PyBoty
type RiskLevel string

const (
	RiskLow      RiskLevel = "LOW"
	RiskMedium   RiskLevel = "MEDIUM"
	RiskHigh     RiskLevel = "HIGH"
	RiskCritical RiskLevel = "CRITICAL"
)

// Policy defines the runtime limits, quotas, and risk classifications.
type Policy struct {
	ToolRiskLevels map[string]RiskLevel
	MaxDailyBudget float64
	BlockedTools   map[string]bool
	Control        ControlPolicy
	WorkspaceRoot  string
	// SessionAutoApprove skips human approval for the current run (local red-team
	// harness). Blocked tools stay blocked.
	SessionAutoApprove bool
}

// requiresApproval is true for HIGH/CRITICAL tools (ADK + Compose middleware share this).
func (p Policy) requiresApproval(toolName string) bool {
	if p.BlockedTools != nil && p.BlockedTools[toolName] {
		return true
	}
	// A tool's own declared risk counts too, so a plugin tool that declares HIGH is
	// held for approval without anyone adding it to the table.
	switch maxRisk(p.ToolRiskLevels[toolName], declaredRisk(toolName)) {
	case RiskHigh, RiskCritical:
		return true
	default:
		return false
	}
}

// GovernanceMiddleware intercepts high-risk tool calls during Agent execution.
type GovernanceMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	queue      *ApprovalQueue
	policy     Policy
	validators *ValidatorChain
	pipeline   *ToolPolicyPipeline
}

// NewGovernanceMiddleware constructs policy only. The queue owns execution
// accounting; rebuilding an agent cannot reset its limits.
func NewGovernanceMiddleware(queue *ApprovalQueue, policy Policy) *GovernanceMiddleware {
	if policy.ToolRiskLevels == nil {
		policy.ToolRiskLevels = make(map[string]RiskLevel)
	}
	if policy.BlockedTools == nil {
		policy.BlockedTools = make(map[string]bool)
	}
	if policy.Control.Mode == "" {
		policy.Control = policy.NormalizeControl()
	}
	return &GovernanceMiddleware{
		queue:      queue,
		policy:     policy,
		validators: DefaultValidatorChain(policy),
		pipeline:   BuildDefaultToolPolicyPipeline(),
	}
}

// PolicySnapshot returns control mode and pipeline stage names for diagnostics UI.
func (m *GovernanceMiddleware) PolicySnapshot() map[string]any {
	if m == nil {
		return nil
	}
	out := map[string]any{
		"control": m.policy.Control.ToMap(),
	}
	if m.pipeline != nil {
		out["pipeline_stages"] = m.pipeline.Describe()
	}
	return out
}

// ComputePayloadHash calculates a deterministic SHA-256 hash for Plan-Hash-Revalidate
func ComputePayloadHash(toolName, argumentsJSON string) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%s|%s", toolName, argumentsJSON)))
	return hex.EncodeToString(h.Sum(nil))
}

// WrapInvokableToolCall intercepts synchronous tool calls and enforces the policy pipeline.
func (m *GovernanceMiddleware) WrapInvokableToolCall(ctx context.Context, endpoint adk.InvokableToolCallEndpoint, tCtx *adk.ToolContext) (adk.InvokableToolCallEndpoint, error) {
	// [Policy Pipeline 1] Hard Block
	if m.policy.BlockedTools[tCtx.Name] {
		return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
			return "", fmt.Errorf("governance: tool %s is strictly blocked by policy", tCtx.Name)
		}, nil
	}

	return func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		ctx = WithCallID(ctx, tCtx.CallID)
		return m.InvokeWithPolicy(ctx, tCtx.Name, argumentsInJSON, func(ctx context.Context, args string) (string, error) {
			return endpoint(ctx, args, opts...)
		})
	}, nil
}

func (m *GovernanceMiddleware) interruptInfo(approvalID, toolName, arguments string) map[string]any {
	return map[string]any{
		"tool":        toolName,
		"approval_id": approvalID,
		"arguments":   arguments,
	}
}

func (m *GovernanceMiddleware) buildDisapprovedResponse(toolName, arguments, reason string) (string, error) {
	resp := map[string]interface{}{
		"status": "rejected", "tool_name": toolName, "arguments": arguments,
		"message": reason,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
