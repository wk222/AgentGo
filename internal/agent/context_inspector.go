package agent

import (
	"context"
	"strings"
	"sync"

	"agentgo/internal/sessions"
)

// ContextSegment represents a token/byte breakdown of a context segment source.
type ContextSegment struct {
	Source     string  `json:"source"` // system_prompt, memory, workspace, tool_output, history, etc.
	TokenCount int     `json:"token_count"`
	ByteCount  int     `json:"byte_count"`
	Percentage float64 `json:"percentage"`
}

// TruncationDecision logs when a segment was pruned due to budget limits.
type TruncationDecision struct {
	Source  string `json:"source"`
	Reason  string `json:"reason"`
	Removed int    `json:"tokens_removed"`
}

// ContextBudgetReport represents the full inspect metrics for a session's token consumption.
type ContextBudgetReport struct {
	TotalTokens   int                  `json:"total_tokens"`
	BudgetLimit   int                  `json:"budget_limit"`
	Segments      []ContextSegment     `json:"segments"`
	TruncationLog []TruncationDecision `json:"truncation_log"`
}

var (
	inspectorMu      sync.RWMutex
	sessionInspector = make(map[string]ContextBudgetReport)
)

// SaveContextBudgetReport records the latest report for the session ID.
func SaveContextBudgetReport(sessionID string, report ContextBudgetReport) {
	inspectorMu.Lock()
	defer inspectorMu.Unlock()
	sessionInspector[sessionID] = report
}

// GetContextBudgetReport retrieves the latest report for the session ID.
func GetContextBudgetReport(sessionID string) (ContextBudgetReport, bool) {
	inspectorMu.RLock()
	defer inspectorMu.RUnlock()
	r, ok := sessionInspector[sessionID]
	return r, ok
}

// InspectContextBudget evaluates token usage segments of the ProjectedRuntimeView against the CanvasPolicy budget.
func InspectContextBudget(ctx context.Context, sessionID string, view *sessions.ProjectedRuntimeView, budgetLimit int) ContextBudgetReport {
	report := ContextBudgetReport{
		BudgetLimit: budgetLimit,
		Segments:    make([]ContextSegment, 0),
	}

	addSegment := func(source string, lines []string) int {
		text := strings.Join(lines, "\n")
		bytes := len(text)
		tokens := estimateTokens(text)
		if tokens > 0 {
			report.Segments = append(report.Segments, ContextSegment{
				Source:     source,
				TokenCount: tokens,
				ByteCount:  bytes,
			})
		}
		return tokens
	}

	total := 0
	total += addSegment("system_rules", view.SystemRules)
	total += addSegment("durable_tasks", view.DurableTasks)
	total += addSegment("memory", view.EpisodicSummary)
	total += addSegment("workflow", view.WorkflowContext)
	total += addSegment("workspace", view.WorkspaceRecent)
	total += addSegment("hygiene", view.ContextHygiene)
	total += addSegment("team_memory", view.TeamMemory)
	total += addSegment("isolation", view.Isolation)
	total += addSegment("tool_output", view.ToolOutputs)

	var histTexts []string
	for _, msg := range view.ActiveTurn {
		histTexts = append(histTexts, msg.Content)
	}
	total += addSegment("history", histTexts)

	report.TotalTokens = total

	for i := range report.Segments {
		if total > 0 {
			report.Segments[i].Percentage = float64(report.Segments[i].TokenCount) / float64(total) * 100.0
		}
	}

	if total > budgetLimit {
		diff := total - budgetLimit
		report.TruncationLog = append(report.TruncationLog, TruncationDecision{
			Source:  "history",
			Reason:  "Context window budget limit exceeded",
			Removed: diff,
		})
	}

	return report
}

func estimateTokens(s string) int {
	runes := []rune(s)
	if len(runes) == 0 {
		return 0
	}
	return len(runes)/3 + 1
}
