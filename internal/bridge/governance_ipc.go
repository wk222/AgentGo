package bridge

import (
	"context"
	"strings"
)

// GetGovernancePolicy returns control preset and pipeline stages for the settings UI.
func (s *AppService) GetGovernancePolicy() map[string]any {
	if s.rt == nil {
		return map[string]any{"success": false, "error": "runtime unavailable"}
	}
	gov := s.rt.GovernanceConfig()
	mode := strings.TrimSpace(gov.ControlMode)
	if mode == "" {
		mode = "balanced"
	}
	return map[string]any{
		"success":      true,
		"control_mode": mode,
		"policy":       s.rt.GovernancePolicySnapshot(),
		"modes":        []string{"strict", "balanced", "open"},
	}
}

// SetGovernanceControlMode persists strict / balanced / open and hot-reloads runner policy.
func (s *AppService) SetGovernanceControlMode(mode string) map[string]any {
	if s.rt == nil {
		return map[string]any{"success": false, "error": "runtime unavailable"}
	}
	mode = strings.TrimSpace(strings.ToLower(mode))
	switch mode {
	case "strict", "balanced", "open":
	default:
		return map[string]any{"success": false, "error": "mode must be strict, balanced, or open"}
	}
	if err := s.rt.SetGovernanceControlMode(mode); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{
		"success":      true,
		"control_mode": mode,
		"policy":       s.rt.GovernancePolicySnapshot(),
	}
}

// QueryGovernanceAudit retrieves the governance audit log for a specific session.
func (s *AppService) QueryGovernanceAudit(sessionID string) ([]map[string]any, error) {
	if s.rt == nil || s.rt.Approvals() == nil {
		return nil, nil
	}
	// Since we can't easily import "agentgo/internal/governance" in the middle, we'll just return map representations.
	entries, err := s.rt.Approvals().QueryAudit(context.Background(), sessionID)
	if err != nil {
		return nil, err
	}
	
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"id":              e.ID,
			"timestamp":       e.Timestamp,
			"channel":         e.Channel,
			"session_id":      e.SessionID,
			"action":          e.Action,
			"tool_name":       e.ToolName,
			"arguments":       e.Arguments,
			"result":          e.Result,
			"risk_level":      e.RiskLevel,
			"policy_snapshot": e.PolicySnapshot,
			"user_id":         e.UserID,
			"explanation":     e.Explanation,
		})
	}
	return out, nil
}
