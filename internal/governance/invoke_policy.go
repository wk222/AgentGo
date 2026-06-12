package governance

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/components/tool"
)

// ToolInvokeFunc executes a tool with JSON arguments (registry / ToolsNode backend).
type ToolInvokeFunc func(ctx context.Context, argumentsJSON string) (string, error)

// InvokeWithPolicy runs the ADK governance pipeline (validators, budget, approval, StatefulInterrupt).
// Used by ADK WrapInvokableToolCall and Compose ToolMiddleware.
func (m *GovernanceMiddleware) InvokeWithPolicy(ctx context.Context, toolName, argumentsInJSON string, endpoint ToolInvokeFunc) (string, error) {
	if m == nil {
		if endpoint == nil {
			return "", fmt.Errorf("governance: nil endpoint")
		}
		return endpoint(ctx, argumentsInJSON)
	}

	if m.policy.BlockedTools[toolName] {
		m.recordAudit(ctx, "policy_deny", toolName, argumentsInJSON, "denied", "CRITICAL", "Tool strictly blocked by static policy")
		return "", fmt.Errorf("governance: tool %s is strictly blocked by policy", toolName)
	}

	isDynamic := isLikelyDynamicTool(toolName, m.policy)
	pipeCtx := PipelineContext(ctx, m.policy, toolName, argumentsInJSON, isDynamic)
	var pipeDecision ToolControlDecision
	if m.pipeline != nil {
		pipeDecision = m.pipeline.Evaluate(pipeCtx)
	} else {
		pipeDecision = m.policy.NormalizeControl().EvaluateToolCall(toolName, isDynamic)
	}
	if !pipeDecision.Allowed {
		reason := pipeDecision.Reason
		if reason == "" {
			reason = "policy pipeline denied"
		}
		m.recordAudit(ctx, "policy_deny", toolName, argumentsInJSON, "denied", string(pipeDecision.Risk), reason)
		return "", fmt.Errorf("governance: %s", reason)
	}

	risk := m.policy.EffectiveRisk(toolName, pipeDecision)

	if err := m.validators.Validate(ctx, toolName, argumentsInJSON, risk); err != nil {
		m.recordAudit(ctx, "validator_deny", toolName, argumentsInJSON, "denied", string(risk), err.Error())
		return "", err
	}

	payloadHash := ComputePayloadHash(toolName, argumentsInJSON)

	if risk == RiskLow && !pipeDecision.RequiresApproval {
		_ = m.budget.CheckAndAdd(0.01, m.policy.MaxDailyBudget)
		out, err := endpoint(ctx, argumentsInJSON)
		resStr := "success"
		if err != nil {
			resStr = "error: " + err.Error()
		}
		m.recordAudit(ctx, "tool_invoke", toolName, argumentsInJSON, resStr, string(risk), "Executed bypass (low risk, no approval required)")
		if err == nil && m.tracker != nil {
			m.tracker.Record(SessionIDFromContext(ctx), toolName)
		}
		return out, err
	}

	if risk == RiskCritical {
		if err := m.budget.CheckAndAdd(1.0, m.policy.MaxDailyBudget); err != nil {
			m.recordAudit(ctx, "budget_deny", toolName, argumentsInJSON, "denied", string(risk), err.Error())
			return "", fmt.Errorf("governance: critical action blocked due to budget: %w", err)
		}
	}

	if !m.policy.RequiresApprovalFor(toolName, pipeDecision) {
		out, err := endpoint(ctx, argumentsInJSON)
		resStr := "success"
		if err != nil {
			resStr = "error: " + err.Error()
		}
		m.recordAudit(ctx, "tool_invoke", toolName, argumentsInJSON, resStr, string(risk), "Executed bypass (policy does not require approval)")
		if err == nil && m.tracker != nil {
			m.tracker.Record(SessionIDFromContext(ctx), toolName)
		}
		return out, err
	}

	wasInterrupted, hasState, st := tool.GetInterruptState[ToolApprovalPause](ctx)
	if wasInterrupted && hasState {
		isResume, hasData, data := tool.GetResumeContext[ResumePayload](ctx)
		if isResume && hasData {
			if data.Approved {
				var finalArgs string
				if data.Arguments != "" {
					finalArgs = data.Arguments
				} else {
					approvedHash := ComputePayloadHash(toolName, st.Arguments)
					if payloadHash != approvedHash {
						m.recordAudit(ctx, "approval_tamper", toolName, st.Arguments, "tamper_error", string(risk), "Tamper detected: Payload hash mismatch after approval")
						return m.buildDisapprovedResponse(toolName, st.Arguments, "Tamper detected: Payload hash mismatch after approval")
					}
					finalArgs = st.Arguments
				}
				if id, ok := m.queue.findApprovedUnconsumed(ctx, payloadHash); ok {
					_ = m.queue.Consume(ctx, id)
				} else {
					_ = m.queue.Consume(ctx, st.ApprovalID)
				}
				out, err := endpoint(ctx, finalArgs)
				resStr := "success"
				if err != nil {
					resStr = "error: " + err.Error()
				}
				m.recordAudit(ctx, "approval_approve", toolName, finalArgs, resStr, string(risk), "Resume executed after user approval")
				if err == nil && m.tracker != nil {
					m.tracker.Record(SessionIDFromContext(ctx), toolName)
				}
				return out, err
			}
			m.recordAudit(ctx, "approval_reject", toolName, st.Arguments, "rejected", string(risk), "Rejected by user")
			return m.buildDisapprovedResponse(toolName, st.Arguments, "Tool execution was rejected by the user")
		}
		return "", tool.StatefulInterrupt(ctx, m.interruptInfo(st.ApprovalID, toolName, st.Arguments), st)
	}

	if id, ok := m.queue.findApprovedUnconsumed(ctx, payloadHash); ok {
		_ = m.queue.Consume(ctx, id)
		out, err := endpoint(ctx, argumentsInJSON)
		resStr := "success"
		if err != nil {
			resStr = "error: " + err.Error()
		}
		m.recordAudit(ctx, "tool_invoke", toolName, argumentsInJSON, resStr, string(risk), "Executed pre-approved unconsumed request")
		if err == nil && m.tracker != nil {
			m.tracker.Record(SessionIDFromContext(ctx), toolName)
		}
		return out, err
	}

	if id, ok := m.queue.findPending(ctx, payloadHash); ok {
		return "", tool.StatefulInterrupt(ctx, m.interruptInfo(id, toolName, argumentsInJSON),
			ToolApprovalPause{ApprovalID: id, ToolName: toolName, Arguments: argumentsInJSON})
	}

	promptMsg := fmt.Sprintf("[%s RISK] Tool call detected: %s with arguments %s. Review required.", risk, toolName, argumentsInJSON)
	approvalReq := NewApprovalRequest(string(KindToolApproval), "agent_runtime", fmt.Sprintf("Approve %s execution", toolName), promptMsg)
	approvalReq.Fingerprint = payloadHash
	approvalReq.Metadata = map[string]interface{}{
		"tool_name": toolName,
		"arguments": argumentsInJSON,
		"risk":      risk,
		"hash":      payloadHash,
	}
	if len(pipeDecision.ControlTags) > 0 {
		approvalReq.PolicyTags = append(approvalReq.PolicyTags, pipeDecision.ControlTags...)
		approvalReq.Metadata["policy_tags"] = pipeDecision.ControlTags
	}
	if pipeDecision.Reason != "" {
		approvalReq.Metadata["policy_reason"] = pipeDecision.Reason
	}
	if err := m.queue.CreateRequest(ctx, approvalReq); err != nil {
		return "", fmt.Errorf("governance: failed to create approval request: %w", err)
	}

	m.recordAudit(ctx, "approval_create", toolName, argumentsInJSON, "interrupted", string(risk), promptMsg)

	pause := ToolApprovalPause{ApprovalID: approvalReq.ID, ToolName: toolName, Arguments: argumentsInJSON}
	return "", tool.StatefulInterrupt(ctx, m.interruptInfo(approvalReq.ID, toolName, argumentsInJSON), pause)
}

func (m *GovernanceMiddleware) recordAudit(ctx context.Context, action, toolName, arguments, result, risk, explanation string) {
	if m == nil || m.queue == nil {
		return
	}
	policySnap := m.PolicySnapshot()
	snapMap := make(map[string]string)
	if policySnap != nil {
		if ctrl, ok := policySnap["control"].(map[string]any); ok {
			for k, v := range ctrl {
				snapMap[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	_ = m.queue.RecordAudit(ctx, AuditEntry{
		Channel:        "governance",
		SessionID:      SessionIDFromContext(ctx),
		Action:         action,
		ToolName:       toolName,
		Arguments:      arguments,
		Result:         result,
		RiskLevel:      risk,
		PolicySnapshot: snapMap,
		Explanation:    explanation,
	})
}
