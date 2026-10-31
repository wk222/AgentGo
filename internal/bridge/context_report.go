package bridge

import (
	"strings"

	"agentgo/internal/agent"
)

// GetContextReport returns the latest context-budget breakdown of a session:
// per-segment totals, the largest individual items, per-turn sizes and advice.
// It is read-only and reports "available: false" until the session has run.
func (s *AppService) GetContextReport(sessionID string) map[string]any {
	sessionID = strings.TrimPrefix(strings.TrimSpace(sessionID), "agentic:")
	if sessionID == "" {
		return map[string]any{"success": true, "available": false}
	}
	report, ok := agent.GetContextBudgetReport(sessionID)
	if !ok {
		return map[string]any{"success": true, "available": false, "session_id": sessionID}
	}
	return map[string]any{"success": true, "available": true, "session_id": sessionID, "report": report}
}
