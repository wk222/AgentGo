package agent

import (
	"fmt"
	"sort"
	"strings"

	"agentgo/internal/sessions"

	"github.com/cloudwego/eino/schema"
)

const (
	maxContextTopItems = 12
	contextWarnPercent = 80.0
)

// ContextItem is one concrete entry (a rule, a memory line, a tool schema, a
// message) so a user can see what is using space, not only which segment.
type ContextItem struct {
	Source     string `json:"source"`
	Label      string `json:"label"`
	TokenCount int    `json:"token_count"`
}

// ContextTurn groups the active conversation by user request, so the largest
// round trip is easy to find.
type ContextTurn struct {
	Index      int    `json:"index"`
	Title      string `json:"title"`
	Messages   int    `json:"messages"`
	TokenCount int    `json:"token_count"`
}

func previewLabel(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	r := []rune(text)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

func flatMessageTokens(m sessions.FlatMessage) int {
	total := 8 + EstimateCalibratedTokens("", m.Content)
	for _, tc := range m.ToolCalls {
		total += 8 + EstimateCalibratedTokens("", tc.Function.Name+tc.Function.Arguments)
	}
	for _, part := range m.UserInputMultiContent {
		if part.Type == schema.ChatMessagePartTypeText {
			if part.Text != m.Content {
				total += EstimateCalibratedTokens("", part.Text)
			}
		} else {
			total += 2000
		}
	}
	return total
}

func flatMessageLabel(m sessions.FlatMessage) string {
	if len(m.ToolCalls) > 0 {
		names := make([]string, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			names = append(names, tc.Function.Name)
		}
		return "assistant tool call: " + strings.Join(names, ", ")
	}
	if m.Role == string(schema.Tool) {
		return "tool result: " + previewLabel(m.Content, 40)
	}
	return m.Role + ": " + previewLabel(m.Content, 40)
}

// collectContextDetail fills TopItems and Turns. Items are sorted by size and
// capped; labels are short previews and never carry full content.
func collectContextDetail(view *sessions.ProjectedRuntimeView, tools []*schema.ToolInfo) ([]ContextItem, []ContextTurn) {
	if view == nil {
		return nil, nil
	}
	var items []ContextItem
	addLines := func(source string, lines []string) {
		for _, line := range lines {
			if n := EstimateCalibratedTokens("", line); n > 0 {
				items = append(items, ContextItem{Source: source, Label: previewLabel(line, 48), TokenCount: n})
			}
		}
	}
	addLines("system_rules", view.SystemRules)
	addLines("durable_tasks", view.DurableTasks)
	addLines("memory", view.EpisodicSummary)
	addLines("task_state", view.TaskState)
	addLines("recalled_memory", view.RecalledMemory)
	addLines("workflow", view.WorkflowContext)
	addLines("workspace", view.WorkspaceRecent)
	addLines("team_memory", view.TeamMemory)
	addLines("tool_output", view.ToolOutputs)
	for _, tool := range tools {
		if tool != nil {
			if n := toolSchemaTokens([]*schema.ToolInfo{tool}); n > 0 {
				items = append(items, ContextItem{Source: "tool_schemas", Label: tool.Name, TokenCount: n})
			}
		}
	}

	var turns []ContextTurn
	for _, m := range view.ActiveTurn {
		n := flatMessageTokens(m)
		items = append(items, ContextItem{Source: "history", Label: flatMessageLabel(m), TokenCount: n})
		if m.Role == string(schema.User) || len(turns) == 0 {
			turns = append(turns, ContextTurn{Index: len(turns) + 1, Title: previewLabel(m.Content, 48)})
		}
		t := &turns[len(turns)-1]
		t.Messages++
		t.TokenCount += n
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].TokenCount > items[j].TokenCount })
	if len(items) > maxContextTopItems {
		items = items[:maxContextTopItems]
	}
	return items, turns
}

// Finalize computes the window percentage and plain-language advice. It can be
// called again after the caller adjusts TotalTokens or the model window.
func (r *ContextBudgetReport) Finalize() {
	r.Advice = nil
	denom := r.ModelContextWindow
	if denom <= 0 {
		denom = r.BudgetLimit
	}
	if denom <= 0 || r.TotalTokens <= 0 {
		r.WindowPercent = 0
		return
	}
	r.WindowPercent = float64(r.TotalTokens) * 100 / float64(denom)
	if r.WindowPercent < contextWarnPercent {
		return
	}
	largest := ContextSegment{}
	for _, s := range r.Segments {
		if s.TokenCount > largest.TokenCount {
			largest = s
		}
	}
	r.Advice = append(r.Advice, fmt.Sprintf("上下文已用 %.0f%%,最大的一类是 %s(%d tokens);建议压缩或开新会话。", r.WindowPercent, largest.Source, largest.TokenCount))
	for _, it := range r.TopItems {
		if it.TokenCount*10 >= r.TotalTokens {
			r.Advice = append(r.Advice, fmt.Sprintf("单条占比超过 10%%:%s / %s(%d tokens)。", it.Source, it.Label, it.TokenCount))
		}
	}
}
