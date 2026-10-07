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
		"run_batch_script":          RiskCritical,
		"replace_workspace_text":    RiskMedium,
		"create_workspace_file":     RiskMedium,
		"create_tool":               RiskHigh,
		"create_tool_from_template": RiskHigh,
		"mcp_filesystem":            RiskHigh,
		"execute_dynamic_tool":      RiskHigh,
		"invoke_subagent":           RiskHigh,
		"run_swarm":                 RiskHigh,
		"workflow_trigger":          RiskMedium,
		"send_email":                RiskMedium,
		// Crush code tools (codetools plugin): mutating ones need care, readers are low risk.
		"code_edit":          RiskMedium,
		"code_multiedit":     RiskMedium,
		"code_write":         RiskMedium,
		"lsp_rename":         RiskMedium,
		"lsp_replace_symbol": RiskMedium,
		"code_view":          RiskLow,
		"code_grep":          RiskLow,
		"code_glob":          RiskLow,
		"code_ls":            RiskLow,
		"lsp_diagnostics":    RiskLow,
		"lsp_references":     RiskLow,
		"lsp_symbols":        RiskLow,
		"lsp_definition":     RiskLow,
		"lsp_call_hierarchy": RiskLow,
		// OpenIPA Distributed RPA & Automation Tools (5 大领域分层治理策略)
		"open_ipa_list_projects":         RiskLow,
		"open_ipa_inspect_project":       RiskLow,
		"open_ipa_validate_project":      RiskLow,
		"open_ipa_inspect_ipa_project":   RiskLow,
		"open_ipa_list_workers":          RiskLow,
		"open_ipa_list_cluster_workers":  RiskLow,
		"open_ipa_get_worker_health":     RiskLow,
		"open_ipa_get_job_detail":        RiskLow,
		"open_ipa_get_cluster_task_status": RiskLow,
		"open_ipa_get_logs":              RiskLow,
		"open_ipa_get_trace":             RiskLow,
		"open_ipa_diagnose_failure":      RiskLow,
		"open_ipa_desktop_screenshot":    RiskLow,
		"open_ipa_window_list":           RiskLow,
		"open_ipa_window_focus":          RiskLow,
		"open_ipa_step_flow":             RiskMedium,
		"open_ipa_run_flow":              RiskMedium,
		"open_ipa_run_ipa_flow":          RiskMedium,
		"open_ipa_cancel_flow":           RiskMedium,
		"open_ipa_execute_step":          RiskMedium,
		"open_ipa_execute_rpa_step":      RiskMedium,
		"open_ipa_desktop_click":         RiskMedium,
		"open_ipa_desktop_type":          RiskMedium,
		"open_ipa_dispatch_job":          RiskMedium,
		"open_ipa_dispatch_cluster_task": RiskMedium,
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
	if p.mutationNeedsApproval(toolName) {
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
	// A declared file-changing tool is never "low": the middleware lets low-risk
	// calls bypass approval before RequiresApprovalFor is even asked.
	if declaredMutating(toolName) {
		risk = maxRisk(risk, RiskMedium)
	}
	return risk
}
