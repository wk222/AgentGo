package agent

import (
	"agentgo/internal/promptbudget"
	"context"
	"strings"
	"sync"

	"agentgo/internal/sessions"
	"github.com/cloudwego/eino/schema"
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
	TotalTokens          int                  `json:"total_tokens"`
	BudgetLimit          int                  `json:"budget_limit"`
	ModelContextWindow   int                  `json:"model_context_window,omitempty"`
	ReservedOutputTokens int                  `json:"reserved_output_tokens,omitempty"`
	Segments             []ContextSegment     `json:"segments"`
	TruncationLog        []TruncationDecision `json:"truncation_log"`
	WindowPercent        float64              `json:"window_percent,omitempty"`
	TopItems             []ContextItem        `json:"top_items,omitempty"`
	Turns                []ContextTurn        `json:"turns,omitempty"`
	Advice               []string             `json:"advice,omitempty"`
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
func InspectContextBudget(ctx context.Context, sessionID string, view *sessions.ProjectedRuntimeView, budgetLimit int, tools ...[]*schema.ToolInfo) ContextBudgetReport {
	report := ContextBudgetReport{
		BudgetLimit: budgetLimit,
		Segments:    make([]ContextSegment, 0),
	}

	addSegment := func(source string, lines []string) int {
		text := strings.Join(lines, "\n")
		bytes := len(text)
		tokens := EstimateCalibratedTokens("", text)
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
	total += addSegment("recalled_memory", view.RecalledMemory)
	total += addSegment("workflow", view.WorkflowContext)
	total += addSegment("workspace", view.WorkspaceRecent)
	total += addSegment("hygiene", view.ContextHygiene)
	total += addSegment("team_memory", view.TeamMemory)
	total += addSegment("isolation", view.Isolation)
	total += addSegment("tool_output", view.ToolOutputs)

	var histTexts []string
	protocol := 0
	for _, msg := range view.ActiveTurn {
		histTexts = append(histTexts, msg.Content)
		protocol += 8
		for _, tc := range msg.ToolCalls {
			protocol += 8 + EstimateCalibratedTokens("", tc.Function.Name+tc.Function.Arguments)
		}
		for _, part := range msg.UserInputMultiContent {
			if part.Type == schema.ChatMessagePartTypeText {
				if part.Text != msg.Content {
					protocol += EstimateCalibratedTokens("", part.Text)
				}
			} else {
				protocol += 2000
			}
		}
	}
	total += addSegment("task_state", view.TaskState)
	total += addSegment("history", histTexts)
	if protocol > 0 {
		report.Segments = append(report.Segments, ContextSegment{Source: "message_protocol_and_media", TokenCount: protocol})
		total += protocol
	}
	if len(tools) > 0 {
		n := toolSchemaTokens(tools[0])
		if n > 0 {
			report.Segments = append(report.Segments, ContextSegment{Source: "tool_schemas", TokenCount: n})
			total += n
		}
	}
	if rendered, err := view.Render(ctx); err == nil {
		n := 0
		for _, msg := range rendered {
			n += messageContextTokens(msg)
		}
		if len(tools) > 0 {
			n += toolSchemaTokens(tools[0])
		}
		if n > total {
			report.Segments = append(report.Segments, ContextSegment{Source: "projection_markup", TokenCount: n - total})
			total = n
		}
	}
	report.TotalTokens = total

	for i := range report.Segments {
		if total > 0 {
			report.Segments[i].Percentage = float64(report.Segments[i].TokenCount) / float64(total) * 100.0
		}
	}

	if total > budgetLimit {
		report.TruncationLog = append(report.TruncationLog, TruncationDecision{
			Source:  "history",
			Reason:  "Estimated context budget exceeded; inspection alone does not truncate messages",
			Removed: 0,
		})
	}

	var toolInfos []*schema.ToolInfo
	if len(tools) > 0 {
		toolInfos = tools[0]
	}
	report.TopItems, report.Turns = collectContextDetail(view, toolInfos)
	report.Finalize()
	return report
}

// Conservative multilingual estimate; actual model usage can raise this via
// calibration. CJK must not be treated as three characters per token.
func estimateTokens(s string) int { return promptbudget.Estimate(s) }
