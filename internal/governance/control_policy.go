package governance

import (
	"strings"
)

// ControlMode mirrors PyBot AgentControlPolicy presets.
type ControlMode string

const (
	ControlStrict   ControlMode = "strict"
	ControlBalanced ControlMode = "balanced"
	ControlOpen     ControlMode = "open"
)

// ControlPolicy is the product-facing governance surface (strict / balanced / open).
type ControlPolicy struct {
	Mode                         ControlMode `json:"mode"`
	BlockedTools                 []string    `json:"blocked_tools,omitempty"`
	BlockedDynamicTools          []string    `json:"blocked_dynamic_tools,omitempty"`
	ApprovalRequiredTools        []string    `json:"approval_required_tools,omitempty"`
	ApprovalRequiredDynamicTools bool        `json:"approval_required_dynamic_tools"`
	AllowDynamicTools            bool        `json:"allow_dynamic_tools"`
	AllowToolMutation            bool        `json:"allow_tool_mutation"`
	AllowAgentDelegation         bool        `json:"allow_agent_delegation"`
	MaxSubagentDepth             int         `json:"max_subagent_depth"`
	MaxCallsPerTool              int         `json:"max_calls_per_tool"`
	StuckLoopWarningThreshold    int         `json:"stuck_loop_warning_threshold"`
	StuckLoopKillThreshold       int         `json:"stuck_loop_kill_threshold"`
}

// ToolControlDecision is one pipeline stage outcome.
type ToolControlDecision struct {
	Allowed          bool
	Risk             RiskLevel
	RequiresApproval bool
	Reason           string
	ControlTags      []string
}

var (
	toolMutationTools  = []string{"create_tool", "create_tool_from_template", "execute_dynamic_tool"}
	hostExecutionTools = []string{"execute_bash", "run_uv_skill"}
	delegationTools    = []string{"invoke_subagent"}
	alwaysApproveTools = []string{"execute_bash", "run_uv_skill"}
)

// ControlPolicyFromMode returns a preset aligned with PyBot agent_control.py.
func ControlPolicyFromMode(mode string) ControlPolicy {
	m := ControlMode(strings.TrimSpace(strings.ToLower(mode)))
	switch m {
	case ControlOpen:
		return ControlPolicy{
			Mode:                      ControlOpen,
			ApprovalRequiredTools:     nil,
			AllowDynamicTools:         true,
			AllowToolMutation:         true,
			AllowAgentDelegation:      true,
			MaxSubagentDepth:          4,
			MaxCallsPerTool:           30,
			StuckLoopWarningThreshold: 4,
			StuckLoopKillThreshold:    8,
		}
	case ControlStrict:
		return ControlPolicy{
			Mode:                      ControlStrict,
			AllowDynamicTools:         false,
			AllowToolMutation:         false,
			AllowAgentDelegation:      false,
			MaxSubagentDepth:          2,
			MaxCallsPerTool:           16,
			StuckLoopWarningThreshold: 2,
			StuckLoopKillThreshold:    4,
		}
	default:
		return ControlPolicy{
			Mode:                         ControlBalanced,
			ApprovalRequiredTools:        append([]string{}, append(toolMutationTools, hostExecutionTools...)...),
			AllowDynamicTools:            true,
			AllowToolMutation:            true,
			AllowAgentDelegation:         true,
			ApprovalRequiredDynamicTools: true,
			MaxSubagentDepth:             3,
			MaxCallsPerTool:              20,
			StuckLoopWarningThreshold:    3,
			StuckLoopKillThreshold:       6,
		}
	}
}

func (c ControlPolicy) blockedSet() map[string]bool {
	out := make(map[string]bool)
	for _, n := range c.BlockedTools {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = true
		}
	}
	return out
}

func (c ControlPolicy) approvalSet() map[string]bool {
	out := make(map[string]bool)
	for _, n := range alwaysApproveTools {
		out[n] = true
	}
	for _, n := range c.ApprovalRequiredTools {
		if n = strings.TrimSpace(n); n != "" {
			out[n] = true
		}
	}
	if c.Mode == ControlBalanced {
		for _, n := range toolMutationTools {
			out[n] = true
		}
		for _, n := range hostExecutionTools {
			out[n] = true
		}
	}
	return out
}

// EvaluateToolCall applies control-policy rules before HITL (risk + allow/deny).
func (c ControlPolicy) EvaluateToolCall(toolName string, isDynamic bool) ToolControlDecision {
	name := strings.TrimSpace(toolName)
	tags := classifyTags(name, isDynamic)
	if name == "" {
		return ToolControlDecision{
			Allowed: false, Risk: RiskHigh, Reason: "工具调用缺少名称", ControlTags: []string{"invalid"},
		}
	}
	if c.blockedSet()[name] {
		return ToolControlDecision{
			Allowed: false, Risk: RiskCritical, Reason: "工具被控制策略禁用: " + name,
			ControlTags: append(tags, "blocked"),
		}
	}
	if isDynamic && !c.AllowDynamicTools {
		return ToolControlDecision{
			Allowed: false, Risk: RiskHigh, Reason: "当前策略禁止执行动态注册工具",
			ControlTags: append(tags, "dynamic-disabled"),
		}
	}
	for _, bt := range c.BlockedDynamicTools {
		if isDynamic && strings.TrimSpace(bt) == name {
			return ToolControlDecision{
				Allowed: false, Risk: RiskHigh, Reason: "动态工具被策略禁用: " + name,
				ControlTags: append(tags, "blocked-dynamic"),
			}
		}
	}
	if contains(toolMutationTools, name) && !c.AllowToolMutation {
		return ToolControlDecision{
			Allowed: false, Risk: RiskCritical, Reason: "当前策略禁止创建/修改工具: " + name,
			ControlTags: append(tags, "tool-mutation-disabled"),
		}
	}
	if contains(delegationTools, name) && !c.AllowAgentDelegation {
		return ToolControlDecision{
			Allowed: false, Risk: RiskCritical, Reason: "当前策略禁止子智能体委派",
			ControlTags: append(tags, "delegation-disabled"),
		}
	}
	risk := classifyRisk(name, isDynamic)
	req := c.approvalSet()[name] || (isDynamic && c.ApprovalRequiredDynamicTools)
	if c.Mode == ControlOpen && !contains(alwaysApproveTools, name) {
		req = false
	}
	return ToolControlDecision{
		Allowed: true, Risk: risk, RequiresApproval: req, ControlTags: tags,
	}
}

func classifyRisk(name string, isDynamic bool) RiskLevel {
	if contains(delegationTools, name) {
		return RiskCritical
	}
	if contains(toolMutationTools, name) || contains(hostExecutionTools, name) {
		return RiskHigh
	}
	if isDynamic {
		return RiskMedium
	}
	return RiskLow
}

func classifyTags(name string, isDynamic bool) []string {
	var tags []string
	if isDynamic {
		tags = append(tags, "dynamic")
	}
	if contains(toolMutationTools, name) {
		tags = append(tags, "tool-mutation")
	}
	if contains(delegationTools, name) {
		tags = append(tags, "delegation")
	}
	if contains(hostExecutionTools, name) {
		tags = append(tags, "host-exec")
	}
	return tags
}

func contains(list []string, name string) bool {
	for _, x := range list {
		if x == name {
			return true
		}
	}
	return false
}

// ToMap exposes policy for settings UI / diagnostics.
func (c ControlPolicy) ToMap() map[string]any {
	return map[string]any{
		"mode":                            string(c.Mode),
		"allow_dynamic_tools":             c.AllowDynamicTools,
		"allow_tool_mutation":             c.AllowToolMutation,
		"allow_agent_delegation":          c.AllowAgentDelegation,
		"approval_required_dynamic_tools": c.ApprovalRequiredDynamicTools,
		"max_subagent_depth":              c.MaxSubagentDepth,
		"max_calls_per_tool":              c.MaxCallsPerTool,
		"stuck_loop_warning_threshold":    c.StuckLoopWarningThreshold,
		"stuck_loop_kill_threshold":       c.StuckLoopKillThreshold,
		"blocked_tools":                   c.BlockedTools,
		"approval_required_tools":         c.ApprovalRequiredTools,
	}
}
