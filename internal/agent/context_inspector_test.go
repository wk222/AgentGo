package agent

import (
	"context"
	"testing"

	"agentgo/internal/sessions"
)

func TestContextBudgetInspector(t *testing.T) {
	view := &sessions.ProjectedRuntimeView{
		SystemRules:     []string{"Rule 1", "Rule 2"},
		DurableTasks:    []string{"Task 1"},
		EpisodicSummary: []string{"Memory fact 1"},
		ActiveTurn: []sessions.FlatMessage{
			{Role: "user", Content: "Hello assistant!"},
			{Role: "assistant", Content: "Hello user! How can I help you today?"},
		},
	}

	ctx := context.Background()
	sessionID := "test_session_inspector"
	budgetLimit := 100

	report := InspectContextBudget(ctx, sessionID, view, budgetLimit)
	if report.BudgetLimit != budgetLimit {
		t.Errorf("expected budget limit %d, got %d", budgetLimit, report.BudgetLimit)
	}

	if report.TotalTokens <= 0 {
		t.Errorf("expected total tokens > 0, got %d", report.TotalTokens)
	}

	// Verify segments exist
	foundRules := false
	foundHistory := false
	for _, seg := range report.Segments {
		if seg.Source == "system_rules" {
			foundRules = true
		}
		if seg.Source == "history" {
			foundHistory = true
		}
		if seg.Percentage <= 0.0 || seg.Percentage > 100.0 {
			t.Errorf("unexpected percentage for segment %s: %f", seg.Source, seg.Percentage)
		}
	}

	if !foundRules {
		t.Error("expected to find system_rules segment in budget report")
	}
	if !foundHistory {
		t.Error("expected to find history segment in budget report")
	}

	// Save and retrieve
	SaveContextBudgetReport(sessionID, report)
	retrieved, exists := GetContextBudgetReport(sessionID)
	if !exists || retrieved.TotalTokens != report.TotalTokens {
		t.Errorf("failed to save/retrieve context budget report: exists=%t, retrieved total=%d, expected=%d", exists, retrieved.TotalTokens, report.TotalTokens)
	}
}
