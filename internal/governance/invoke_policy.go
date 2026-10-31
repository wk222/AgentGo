package governance

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

// ToolInvokeFunc executes a tool with JSON arguments (registry / ToolsNode backend).
type ToolInvokeFunc func(ctx context.Context, argumentsJSON string) (string, error)

// InvokeWithPolicy runs the ADK governance pipeline (validators, budget, approval, StatefulInterrupt).
// Used by ADK WrapInvokableToolCall and Compose ToolMiddleware.
func (m *GovernanceMiddleware) InvokeWithPolicy(ctx context.Context, toolName, argumentsInJSON string, endpoint ToolInvokeFunc) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if endpoint == nil {
		return "", fmt.Errorf("governance: nil endpoint")
	}
	if m == nil {
		if endpoint == nil {
			return "", fmt.Errorf("governance: nil endpoint")
		}
		return endpoint(ctx, argumentsInJSON)
	}
	wasInterrupted, hasState, st := tool.GetInterruptState[ToolApprovalPause](ctx)
	ctx = withToolDefinition(ctx, toolName)
	if wasInterrupted && hasState {
		if st.DefinitionID != ToolDefinitionFromContext(ctx) {
			return "", fmt.Errorf("governance: tool definition changed; request fresh approval")
		}
		ctx = restoreExecution(ctx, st.ExecutionID)
	}

	if m.policy.BlockedTools[toolName] {
		m.recordAudit(ctx, "policy_deny", toolName, argumentsInJSON, "denied", "CRITICAL", "Tool strictly blocked by static policy")
		return "", fmt.Errorf("governance: tool %s is strictly blocked by policy", toolName)
	}

	isDynamic := isLikelyDynamicTool(toolName, m.policy)
	pipeCtx := PipelineContext(ctx, m.policy, toolName, argumentsInJSON, isDynamic)
	if m.queue != nil {
		n, err := m.queue.recentCalls(ctx, executionID(ctx, m.policy.WorkspaceRoot), toolName)
		if err != nil {
			return "", fmt.Errorf("governance: read execution counters: %w", err)
		}
		pipeCtx.RecentCalls = n
	}
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
		out, err := m.execute(ctx, toolName, argumentsInJSON, risk, endpoint, "", payloadHash, false)
		resStr := "success"
		if err != nil {
			resStr = "error: " + err.Error()
		}
		m.recordAudit(ctx, "tool_invoke", toolName, argumentsInJSON, resStr, string(risk), "Executed bypass (low risk, no approval required)")
		return out, err
	}

	if !m.policy.RequiresApprovalFor(toolName, pipeDecision) {
		out, err := m.execute(ctx, toolName, argumentsInJSON, risk, endpoint, "", payloadHash, len(pipeDecision.GrantDirs) > 0)
		resStr := "success"
		if err != nil {
			resStr = "error: " + err.Error()
		}
		m.recordAudit(ctx, "tool_invoke", toolName, argumentsInJSON, resStr, string(risk), "Executed bypass (policy does not require approval)")
		return out, err
	}

	if m.queue == nil {
		return "", fmt.Errorf("governance: approval queue unavailable")
	}
	// The user already said "allow this" for the rest of the conversation.
	// CRITICAL calls and calls that open folders outside the workspace always
	// ask again.
	if !wasInterrupted && risk != RiskCritical && len(pipeDecision.GrantDirs) == 0 &&
		IsRemembered(SessionIDFromContext(ctx), toolName, argumentsInJSON) {
		out, err := m.execute(ctx, toolName, argumentsInJSON, risk, endpoint, "", payloadHash, false)
		resStr := "success"
		if err != nil {
			resStr = "error: " + err.Error()
		}
		m.recordAudit(ctx, "approval_remembered", toolName, argumentsInJSON, resStr, string(risk), "Executed under an earlier approval remembered for this session")
		return out, err
	}
	if wasInterrupted && hasState {
		isResume, hasData, data := resumePayloadFrom(ctx)
		if isResume && hasData {
			if data.Approved {
				if st.ToolName != toolName || ComputePayloadHash(toolName, st.Arguments) != payloadHash {
					return "", fmt.Errorf("governance: resumed invocation does not match the approved tool and arguments")
				}
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
				if err := m.validateApprovedArguments(ctx, toolName, finalArgs); err != nil {
					return "", err
				}
				out, err := m.execute(ctx, toolName, finalArgs, risk, endpoint, st.ApprovalID, payloadHash, true)
				resStr := "success"
				if err != nil {
					resStr = "error: " + err.Error()
				}
				m.recordAudit(ctx, "approval_approve", toolName, finalArgs, resStr, string(risk), "Resume executed after user approval")
				return out, err
			}
			m.recordAudit(ctx, "approval_reject", toolName, st.Arguments, "rejected", string(risk), "Rejected by user")
			return m.buildDisapprovedResponse(toolName, st.Arguments, "Tool execution was rejected by the user")
		}
		return "", tool.StatefulInterrupt(ctx, m.interruptInfo(st.ApprovalID, toolName, st.Arguments), st)
	}

	if id, ok := m.queue.findApprovedUnconsumed(ctx, payloadHash, m.policy.WorkspaceRoot); ok {
		out, err := m.execute(ctx, toolName, argumentsInJSON, risk, endpoint, id, payloadHash, true)
		resStr := "success"
		if err != nil {
			resStr = "error: " + err.Error()
		}
		m.recordAudit(ctx, "tool_invoke", toolName, argumentsInJSON, resStr, string(risk), "Executed pre-approved unconsumed request")
		return out, err
	}

	if id, ok := m.queue.findPending(ctx, payloadHash, m.policy.WorkspaceRoot); ok {
		return "", tool.StatefulInterrupt(ctx, m.interruptInfo(id, toolName, argumentsInJSON),
			ToolApprovalPause{ApprovalID: id, ToolName: toolName, Arguments: argumentsInJSON, ExecutionID: executionID(ctx, m.policy.WorkspaceRoot), DefinitionID: ToolDefinitionFromContext(ctx)})
	}

	promptMsg := fmt.Sprintf("[%s RISK] Tool call detected: %s with arguments %s. Review required.", risk, toolName, argumentsInJSON)
	if pipeDecision.Reason != "" {
		promptMsg += "\n" + pipeDecision.Reason
	}
	approvalReq := NewApprovalRequest(string(KindToolApproval), "agent_runtime", fmt.Sprintf("Approve %s execution", toolName), promptMsg)
	approvalReq.Fingerprint = payloadHash
	approvalReq.Metadata = map[string]interface{}{
		"tool_definition": ToolDefinitionFromContext(ctx),
		"execution_id":    executionID(ctx, m.policy.WorkspaceRoot),
		"session_id":      SessionIDFromContext(ctx),
		"workspace":       m.policy.WorkspaceRoot,
		"call_id":         callIDFrom(ctx),
		"tool_name":       toolName,
		"arguments":       argumentsInJSON,
		"risk":            risk,
		"hash":            payloadHash,
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

	pause := ToolApprovalPause{ApprovalID: approvalReq.ID, ToolName: toolName, Arguments: argumentsInJSON, ExecutionID: executionID(ctx, m.policy.WorkspaceRoot), DefinitionID: ToolDefinitionFromContext(ctx)}
	return "", tool.StatefulInterrupt(ctx, m.interruptInfo(approvalReq.ID, toolName, argumentsInJSON), pause)
}

// All paths to a real tool share this boundary. Veto/cancellation happens
// before admission; admission/approval/counters are committed together.
func (m *GovernanceMiddleware) execute(ctx context.Context, name, args string, risk RiskLevel, endpoint ToolInvokeFunc, approvalID, fingerprint string, grant bool) (string, error) {
	var ticket string
	entered := false
	run := m.withHooks(name, func(ctx context.Context, args string) (string, error) {
		entered = true
		if grant {
			GrantOutsideDirs(m.policy.WorkspaceRoot, args)
		}
		return endpoint(ctx, args)
	}, func(ctx context.Context, _ string) error {
		var err error
		ticket, err = m.queue.admitExecution(ctx, executionID(ctx, m.policy.WorkspaceRoot), name, risk, m.policy, approvalID, fingerprint)
		if err != nil {
			m.recordAudit(ctx, "execution_deny", name, args, "denied", string(risk), err.Error())
		}
		return err
	})
	out, err := run(ctx, args)
	if ticket != "" {
		if _, interrupted := compose.IsInterruptRerunError(err); interrupted || !entered {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if releaseErr := m.queue.releaseExecutionReservation(releaseCtx, ticket); releaseErr != nil {
				return "", fmt.Errorf("governance: release paused execution: %w", releaseErr)
			}
		}
	}
	return out, err
}

func (m *GovernanceMiddleware) recordAudit(ctx context.Context, action, toolName, arguments, result, risk, explanation string) {
	notifyAuditObserver(ctx, action, toolName, result, risk, explanation)
	if m == nil || m.queue == nil {
		return
	}
	policySnap := m.PolicySnapshot()
	snapMap := make(map[string]string)
	snapMap["execution_id"] = executionID(ctx, m.policy.WorkspaceRoot)
	if definition := ToolDefinitionFromContext(ctx); definition != "" {
		snapMap["tool_definition"] = definition
	}
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

func (m *GovernanceMiddleware) validateApprovedArguments(ctx context.Context, name, args string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	decision := m.pipeline.Evaluate(PipelineContext(ctx, m.policy, name, args, isLikelyDynamicTool(name, m.policy)))
	if !decision.Allowed {
		return fmt.Errorf("governance: approved arguments denied by current policy: %s", decision.Reason)
	}
	return m.validators.Validate(ctx, name, args, m.policy.EffectiveRisk(name, decision))
}
