package governance

import "strings"

// NormalizeControl fills zero-value Control with balanced preset (legacy tests / bare Policy{}).
func (p Policy) NormalizeControl() ControlPolicy {
	if p.Control.Mode != "" {
		return p.Control
	}
	return ControlPolicyFromMode(string(ControlBalanced))
}

// BuildPolicy merges control preset + static risk table for runtime middleware.
func BuildPolicy(mode string, workspaceRoot string) Policy {
	ctrl := ControlPolicyFromMode(mode)
	p := Policy{
		MaxDailyBudget: 100.0,
		Control:        ctrl,
		WorkspaceRoot:  strings.TrimSpace(workspaceRoot),
		BlockedTools:   ctrl.blockedSet(),
		ToolRiskLevels: defaultRiskTable(),
	}
	applyControlToRiskLevels(&p, ctrl)
	return p
}

func defaultRiskTable() map[string]RiskLevel {
	return map[string]RiskLevel{
		"execute_bash":              RiskCritical,
		"run_uv_skill":              RiskCritical,
		"create_tool":               RiskHigh,
		"create_tool_from_template": RiskHigh,
		"mcp_filesystem":            RiskHigh,
		"execute_dynamic_tool":      RiskHigh,
		"invoke_subagent":           RiskHigh,
		"run_swarm":                 RiskHigh,
		"workflow_trigger":          RiskMedium,
		"send_email":                RiskMedium,
	}
}

func applyControlToRiskLevels(p *Policy, ctrl ControlPolicy) {
	if p == nil {
		return
	}
	if p.ToolRiskLevels == nil {
		p.ToolRiskLevels = make(map[string]RiskLevel)
	}
	for _, name := range alwaysApproveTools {
		p.ToolRiskLevels[name] = RiskCritical
	}
	if ctrl.Mode == ControlStrict {
		for _, name := range toolMutationTools {
			p.BlockedTools[name] = true
		}
		if !ctrl.AllowAgentDelegation {
			p.BlockedTools["invoke_subagent"] = true
		}
	}
	approval := ctrl.approvalSet()
	for name := range approval {
		if lvl := p.ToolRiskLevels[name]; lvl == "" || lvl == RiskLow {
			p.ToolRiskLevels[name] = RiskHigh
		}
	}
}

// RequiresApprovalFor decides HITL after pipeline + static policy.
func (p Policy) RequiresApprovalFor(toolName string, pipeline ToolControlDecision) bool {
	if p.BlockedTools != nil && p.BlockedTools[toolName] {
		return true
	}
	if contains(alwaysApproveTools, toolName) {
		return true
	}
	if pipeline.RequiresApproval {
		return true
	}
	return p.requiresApproval(toolName)
}

// EffectiveRisk returns merged risk from pipeline and static map.
func (p Policy) EffectiveRisk(toolName string, pipeline ToolControlDecision) RiskLevel {
	risk := pipeline.Risk
	if risk == "" {
		risk = RiskLow
	}
	if static := p.ToolRiskLevels[toolName]; static != "" {
		risk = maxRisk(risk, static)
	}
	return risk
}
