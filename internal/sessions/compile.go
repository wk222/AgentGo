package sessions

import (
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// CompileOptions configures message → ProjectedRuntimeView classification.
type CompileOptions struct {
	MaxActiveTurns int
	// RecentTailTurns controls how many most-recent messages keep full tool outputs
	// before Reasonix-style deterministic head/tail snipping applies (default 6).
	RecentTailTurns int
	// MaxStaleToolChars is the character threshold above which older tool results
	// are deterministically snipped to preserve prompt prefix cache economics (default 1600).
	MaxStaleToolChars int
	DurableTasks      []string
	WorkspaceRules    string // SOUL/TEAM/rules preloaded (optional)
	Snapshot          SpineSnapshot
}

const (
	tagSystemSummary   = "[System Summary]"
	tagMemoryInject    = "[Injected Memory Context]"
	tagWorkspace       = "[Workspace Context]"
	tagEnvFeedback     = "[Environment Feedback]"
	tagWorkflowContext = "[Workflow Context]"
	tagAppMatrix       = "[App Matrix]"
	tagContextHygiene  = "[Context Hygiene]"
	tagTeamMemory      = "[Team Memory]"
	tagIsolation       = "[Isolation]"
	tagModeProfile     = "[Mode Profile]"
)

// CompileProjectedView classifies flat ADK messages into the six-section runtime projection.
func CompileProjectedView(messages []*schema.Message, opt CompileOptions) *ProjectedRuntimeView {
	if opt.MaxActiveTurns <= 0 {
		opt.MaxActiveTurns = 30
	}
	view := &ProjectedRuntimeView{
		DurableTasks:    append([]string(nil), opt.DurableTasks...),
		WorkflowContext: append([]string(nil), opt.Snapshot.WorkflowContext...),
		EpisodicSummary: append([]string(nil), opt.Snapshot.EpisodicSummary...),
		ContextHygiene:  append([]string(nil), opt.Snapshot.ContextHygiene...),
		TeamMemory:      append([]string(nil), opt.Snapshot.TeamMemory...),
		Isolation:       append([]string(nil), opt.Snapshot.Isolation...),
	}
	if rules := strings.TrimSpace(opt.WorkspaceRules); rules != "" {
		view.SystemRules = append(view.SystemRules, rules)
	}
	if len(opt.Snapshot.WorkspaceRecent) > 0 {
		view.WorkspaceRecent = append([]string(nil), opt.Snapshot.WorkspaceRecent...)
	}

	var active []FlatMessage
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" && len(msg.ToolCalls) == 0 {
			continue
		}

		switch {
		case msg.Role == schema.System:
			// A message rendered by an earlier compile of this run carries its raw input.
			// Re-reading its rendered text would wrap <system_rules> again and again and
			// fold every block back into the rules, growing the prompt on each model call.
			raws := []string{content}
			if base, ok := projectedBase(msg); ok {
				raws = base
			}
			for _, raw := range raws {
				view.baseSystem = append(view.baseSystem, raw)
				view.SystemRules = append(view.SystemRules, classifySystemContent(raw)...)
			}

		case msg.Role == schema.User:
			switch {
			case strings.HasPrefix(content, tagSystemSummary):
				view.EpisodicSummary = append(view.EpisodicSummary, strings.TrimSpace(strings.TrimPrefix(content, tagSystemSummary)))
			case strings.HasPrefix(content, tagMemoryInject):
				view.EpisodicSummary = append(view.EpisodicSummary, strings.TrimSpace(strings.TrimPrefix(content, tagMemoryInject)))
			case strings.HasPrefix(content, tagEnvFeedback):
				view.ToolOutputs = append(view.ToolOutputs, strings.TrimSpace(strings.TrimPrefix(content, tagEnvFeedback)))
			case strings.HasPrefix(content, tagWorkflowContext), strings.HasPrefix(content, tagAppMatrix):
				view.WorkflowContext = append(view.WorkflowContext, stripKnownPrefix(content))
			case strings.HasPrefix(content, tagContextHygiene):
				view.ContextHygiene = append(view.ContextHygiene, stripKnownPrefix(content))
			case strings.HasPrefix(content, tagTeamMemory):
				view.TeamMemory = append(view.TeamMemory, stripKnownPrefix(content))
			case strings.HasPrefix(content, tagIsolation), strings.HasPrefix(content, tagModeProfile):
				view.Isolation = append(view.Isolation, stripKnownPrefix(content))
			default:
				active = append(active, flatFromSchema(msg))
			}

		case msg.Role == schema.Assistant:
			active = append(active, flatFromSchema(msg))

		case msg.Role == schema.Tool:
			fm := flatFromSchema(msg)
			fm.Role = "tool"
			fm.ToolCallID = msg.ToolCallID
			active = append(active, fm)
		}
	}

	var dropped int
	active, dropped = pruneAndNormalizeActive(active, opt.MaxActiveTurns)
	var snipped int
	active, snipped = snipStaleToolResults(active, opt.RecentTailTurns, opt.MaxStaleToolChars)
	if dropped > 0 || snipped > 0 {
		view.ContextHygiene = append(view.ContextHygiene,
			fmt.Sprintf("活跃窗口保留最近 %d 条消息（裁剪较早 %d 条，确定性折叠旧工具长输出 %d 处以保护 Prefix Cache；需要更早细节请参考 episodic_summary）。", len(active), dropped, snipped))
	}
	view.ActiveTurn = active
	return view
}

// snipStaleToolResults implements Reasonix SPEC §3.6 tiered tool-result snipping:
// Tool outputs before the recent tail that exceed maxChars are shortened with deterministic
// head/tail markers while preserving message pairing and keeping error/failed outputs intact.
func snipStaleToolResults(active []FlatMessage, recentTail int, maxChars int) ([]FlatMessage, int) {
	if len(active) == 0 {
		return active, 0
	}
	if recentTail <= 0 {
		recentTail = 6
	}
	if maxChars <= 0 {
		maxChars = 1600
	}
	cutoff := len(active) - recentTail
	if cutoff <= 0 {
		return active, 0
	}

	snippedCount := 0
	out := make([]FlatMessage, len(active))
	copy(out, active)

	headKeep := maxChars * 5 / 8
	tailKeep := maxChars - headKeep

	for i := 0; i < cutoff; i++ {
		if out[i].Role != "tool" {
			continue
		}
		content := out[i].Content
		if len(content) <= maxChars {
			continue
		}
		// KeepErrors: preserve error or blocked tool outputs so the model does not lose failure diagnostics
		if looksLikeErrorToolOutput(content) {
			continue
		}
		omitted := len(content) - (headKeep + tailKeep)
		if omitted <= 120 {
			continue
		}
		out[i].Content = fmt.Sprintf(
			"%s\n...[snip: %d chars omitted for prefix-cache hygiene]...\n%s",
			content[:headKeep],
			omitted,
			content[len(content)-tailKeep:],
		)
		snippedCount++
	}
	return out, snippedCount
}

func looksLikeErrorToolOutput(content string) bool {
	lower := strings.ToLower(content)
	if len(lower) > 512 {
		lower = lower[:512]
	}
	return strings.Contains(lower, "\"error\"") ||
		strings.Contains(lower, "error:") ||
		strings.Contains(lower, "panic:") ||
		strings.Contains(lower, "failed:") ||
		strings.Contains(lower, "exit code: 1") ||
		strings.Contains(lower, "blocked")
}

// pruneAndNormalizeActive prunes messages exceeding maxTurns while strictly preserving
// atomic tool_call <-> tool_result pairing invariant required by LLM providers (Codex parity).
func pruneAndNormalizeActive(active []FlatMessage, maxTurns int) ([]FlatMessage, int) {
	if len(active) <= maxTurns {
		normalized := normalizeToolPairs(active)
		return normalized, len(active) - len(normalized)
	}

	start := len(active) - maxTurns
	// Never start slice with an orphaned tool response
	for start < len(active) && active[start].Role == "tool" {
		start++
	}

	candidate := active[start:]
	normalized := normalizeToolPairs(candidate)
	dropped := len(active) - len(normalized)
	return normalized, dropped
}

// normalizeToolPairs ensures every tool message has a preceding assistant with matching ToolCallID,
// and every assistant ToolCall has a following tool message. Any orphaned items are safely filtered out.
func normalizeToolPairs(msgs []FlatMessage) []FlatMessage {
	if len(msgs) == 0 {
		return msgs
	}

	// 1. Identify which tool_call_ids actually have a response in the window
	toolResponses := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			toolResponses[m.ToolCallID] = true
		}
	}

	// 2. Identify which assistant messages have tool calls
	knownCalls := make(map[string]bool)
	for _, m := range msgs {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					knownCalls[tc.ID] = true
				}
			}
		}
	}

	// 3. Filter: drop tool responses without parent call; drop assistant tool calls without responses
	result := make([]FlatMessage, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == "tool" {
			// Orphaned tool response whose parent assistant call was dropped
			if !knownCalls[m.ToolCallID] {
				continue
			}
			result = append(result, m)
		} else if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			var validCalls []schema.ToolCall
			for _, tc := range m.ToolCalls {
				if toolResponses[tc.ID] {
					validCalls = append(validCalls, tc)
				}
			}
			// If all tool calls were dropped and content is empty, skip this empty assistant msg
			if len(validCalls) == 0 && strings.TrimSpace(m.Content) == "" {
				continue
			}
			m.ToolCalls = validCalls
			result = append(result, m)
		} else {
			result = append(result, m)
		}
	}
	return result
}

func flatFromSchema(msg *schema.Message) FlatMessage {
	fm := FlatMessage{
		Role:    string(msg.Role),
		Content: strings.TrimSpace(msg.Content),
	}
	if len(msg.ToolCalls) > 0 {
		fm.ToolCalls = append([]schema.ToolCall(nil), msg.ToolCalls...)
	}
	if msg.Role == schema.Tool {
		fm.ToolCallID = msg.ToolCallID
	}
	if len(msg.UserInputMultiContent) > 0 {
		fm.UserInputMultiContent = append([]schema.MessageInputPart(nil), msg.UserInputMultiContent...)
	}
	return fm
}

func classifySystemContent(content string) []string {
	c := strings.TrimSpace(content)
	if strings.HasPrefix(c, tagWorkspace) {
		return []string{strings.TrimSpace(strings.TrimPrefix(c, tagWorkspace))}
	}
	if strings.HasPrefix(c, tagMemoryInject) {
		// memory sometimes lands in system merge from other MW — treat as rules + summary split
		body := strings.TrimSpace(strings.TrimPrefix(c, tagMemoryInject))
		return []string{body}
	}
	return []string{c}
}

func stripKnownPrefix(content string) string {
	for _, p := range []string{tagWorkflowContext, tagAppMatrix, tagContextHygiene, tagTeamMemory, tagIsolation, tagModeProfile} {
		if strings.HasPrefix(content, p) {
			return strings.TrimSpace(strings.TrimPrefix(content, p))
		}
	}
	return content
}
