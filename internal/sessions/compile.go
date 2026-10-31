package sessions

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/eino/schema"
)

// CompileOptions configures message → ProjectedRuntimeView classification.
type CompileOptions struct {
	// RecallTokenBudget is the already planned recall budget; nil retains the legacy default, zero clears recall.
	RecallTokenBudget  *int
	KeepActiveHistory  bool // normal path: let official summarization own semantic compaction
	PlanTasks          []PlanTaskSnapshot
	MaxActiveTurns     int
	PreserveLatestUser bool
	// HardToolResultChars is used only under budget pressure, including recent/error outputs.
	HardToolResultChars int
	// RecentTailTurns controls how many most-recent messages keep full tool outputs
	// before deterministic head/tail snipping applies (default 6).
	RecentTailTurns int
	// MaxStaleToolChars is the character threshold above which older tool results
	// are snipped under explicit budget pressure; zero leaves recorded outputs intact.
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
		recallTokenBudget: opt.RecallTokenBudget,
		DurableTasks:      append([]string(nil), opt.DurableTasks...),
		WorkflowContext:   append([]string(nil), opt.Snapshot.WorkflowContext...),
		EpisodicSummary:   append([]string(nil), opt.Snapshot.EpisodicSummary...),
		ContextHygiene:    append([]string(nil), opt.Snapshot.ContextHygiene...),
		TeamMemory:        append([]string(nil), opt.Snapshot.TeamMemory...),
		Isolation:         append([]string(nil), opt.Snapshot.Isolation...),
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
		if content == "" && len(msg.ToolCalls) == 0 && len(msg.UserInputMultiContent) == 0 && msg.Role != schema.Tool && !(msg.Role == schema.System && msg.Extra["agentgo.memory_context"] == true) {
			continue
		}

		switch {
		case msg.Role == schema.System:
			if msg.Extra != nil && msg.Extra["agentgo.memory_context"] == true {
				view.freshRecall = true
				if revision, ok := msg.Extra[ExtraMemoryRevision].(string); ok {
					view.memory.MemoryRevision = revision
				}
				text := strings.TrimSpace(strings.TrimPrefix(content, tagMemoryInject))
				if text != "" {
					view.RecalledMemory = append(view.RecalledMemory, text)
				}
				continue
			}
			if msg.Extra != nil && msg.Extra["agentgo.workspace_context"] == true {
				continue
			}
			view.inheritMemory(msg)
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
			if !IsContextMessage(msg) {
				active = append(active, flatFromSchema(msg))
				continue
			}
			switch ContextKind(msg) {
			case "runtime":
				view.inheritMemory(msg)
				continue // regenerated from the current snapshot and persisted projection memory
			case "summary":
				if revision, ok := msg.Extra[ExtraMemoryRevision].(string); ok {
					view.memory.MemoryRevision = revision
				}
				view.EpisodicSummary = append(view.EpisodicSummary, strings.TrimSpace(strings.TrimPrefix(content, tagSystemSummary)))
			case "memory":
				view.freshRecall = true
				if revision, ok := msg.Extra[ExtraMemoryRevision].(string); ok {
					view.memory.MemoryRevision = revision
				}
				view.RecalledMemory = append(view.RecalledMemory, strings.TrimSpace(strings.TrimPrefix(content, tagMemoryInject)))
			case "feedback":
				view.ToolOutputs = append(view.ToolOutputs, strings.TrimSpace(strings.TrimPrefix(content, tagEnvFeedback)))
			case "workflow", "app_matrix":
				view.WorkflowContext = append(view.WorkflowContext, stripKnownPrefix(content))
			case "hygiene":
				view.ContextHygiene = append(view.ContextHygiene, stripKnownPrefix(content))
			case "team":
				view.TeamMemory = append(view.TeamMemory, stripKnownPrefix(content))
			case "isolation", "mode":
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

	// Repair protocol history before either truncation or recovery-ledger creation.
	// Invalid/duplicate results must not reappear later as remembered facts.
	active = normalizeToolPairs(active)
	rawActive := active
	var dropped int
	if opt.KeepActiveHistory {
		opt.MaxActiveTurns = len(active)
	}
	active, dropped = pruneAndNormalizeActive(active, opt.MaxActiveTurns)
	if opt.PreserveLatestUser {
		for i := len(rawActive) - 1; i >= 0; i-- {
			if rawActive[i].Role == "user" {
				found := false
				for _, m := range active {
					if m.Role == "user" && m.Content == rawActive[i].Content {
						found = true
						break
					}
				}
				if !found {
					active = append([]FlatMessage{rawActive[i]}, active...)
					dropped = max(0, dropped-1)
				}
				break
			}
		}
	}
	view.rememberCompaction(rawActive, active, dropped)
	view.applyPlanTasks(opt.PlanTasks)
	var snipped int
	if opt.MaxStaleToolChars > 0 {
		active, snipped = snipStaleToolResults(active, opt.RecentTailTurns, opt.MaxStaleToolChars)
	}
	if dropped > 0 || snipped > 0 {
		view.ContextHygiene = append(view.ContextHygiene,
			fmt.Sprintf("活跃窗口保留最近 %d 条消息（裁剪较早 %d 条，确定性折叠旧工具长输出 %d 处以控制上下文预算；需要更早细节请参考 episodic_summary）。", len(active), dropped, snipped))
	}
	if opt.HardToolResultChars > 0 {
		for i := range active {
			if active[i].Role == "tool" && len(active[i].Content) > opt.HardToolResultChars {
				n := opt.HardToolResultChars
				content := active[i].Content
				active[i].Content = validSnippetHead(content, n*2/3) + "\n...[context budget: tool result excerpt; retrieve full output from session/spill if needed]...\n" + validSnippetTail(content, n/3)
			}
		}
	}
	view.ActiveTurn = active
	return view
}

// buildCompactionCheckpoint constructs a zero-LLM deterministic state recovery summary
// (absorbed from a reference implementation) when older turns are
// pruned or snipped: it preserves the initial user goal anchor and a compact ledger of
// earlier tool actions so the model never loses original constraints or touched targets.
func buildCompactionCheckpoint(rawActive, keptActive []FlatMessage) string {
	if len(rawActive) == 0 {
		return ""
	}
	var parts []string

	// 1. Initial user goal anchor if the earliest user turn was pruned out of keptActive.
	firstUser := ""
	for _, m := range rawActive {
		if m.Role == "user" || m.Role == string(schema.User) {
			firstUser = collapseOneLine(m.Content, 220)
			break
		}
	}
	if firstUser != "" {
		stillPresent := false
		for _, m := range keptActive {
			if (m.Role == "user" || m.Role == string(schema.User)) && collapseOneLine(m.Content, 220) == firstUser {
				stillPresent = true
				break
			}
		}
		if !stillPresent {
			parts = append(parts, "初始目标锚点: "+firstUser)
		}
	}

	// 2. Deterministic tool action ledger from pruned/older turns.
	cutoff := len(rawActive) - len(keptActive)
	if cutoff < len(rawActive)/2 {
		cutoff = len(rawActive) / 2
	}
	var actions []string
	seen := make(map[string]bool)
	for i := 0; i < cutoff && i < len(rawActive); i++ {
		m := rawActive[i]
		if m.Role != "assistant" && m.Role != string(schema.Assistant) {
			continue
		}
		for _, tc := range m.ToolCalls {
			entry := summarizeToolCallForCheckpoint(tc.Function.Name, tc.Function.Arguments)
			if entry != "" && !seen[entry] {
				seen[entry] = true
				actions = append(actions, entry)
			}
		}
	}
	if len(actions) > 8 {
		actions = actions[len(actions)-8:]
	}
	if len(actions) > 0 {
		parts = append(parts, "工具调用轨迹（不代表完成或成功）: "+strings.Join(actions, " -> "))
	}

	if len(parts) == 0 {
		return ""
	}
	return "[Compaction Checkpoint] " + strings.Join(parts, " | ")
}

func summarizeToolCallForCheckpoint(name, rawArgs string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &args); err == nil && len(args) > 0 {
		for _, k := range []string{"path", "relative_path", "file", "command", "pattern", "url", "case", "tool", "action"} {
			if v, ok := args[k]; ok {
				if s := collapseOneLine(fmt.Sprint(v), 48); s != "" {
					return fmt.Sprintf("%s(%s)", name, s)
				}
			}
		}
	}
	return name
}

func collapseOneLine(s string, maxRunes int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if maxRunes <= 0 || s == "" {
		return s
	}
	r := []rune(s)
	if len(r) > maxRunes {
		return string(r[:maxRunes]) + "…"
	}
	return s
}

// snipStaleToolResults implements tiered tool-result snipping:
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
			"%s\n...[snip: %d chars omitted for context budget]...\n%s",
			validSnippetHead(content, headKeep),
			omitted,
			validSnippetTail(content, tailKeep),
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
		return normalized, max(0, len(active)-len(normalized))
	}

	start := len(active) - maxTurns
	// Never start slice with an orphaned tool response
	for start < len(active) && active[start].Role == "tool" {
		start++
	}

	candidate := active[start:]
	normalized := normalizeToolPairs(candidate)
	dropped := max(0, len(active)-len(normalized))
	return normalized, dropped
}

// normalizeToolPairs preserves contiguous call/result groups. Missing results are
// prompt-only unknown outcomes, never evidence of execution or success.
func normalizeToolPairs(msgs []FlatMessage) []FlatMessage {
	result := make([]FlatMessage, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == "tool" {
			continue
		} // orphan, duplicate, or late result
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			result = append(result, m)
			continue
		}
		calls := make([]schema.ToolCall, 0, len(m.ToolCalls))
		allowed := map[string]bool{}
		for _, tc := range m.ToolCalls {
			if tc.ID == "" || allowed[tc.ID] {
				continue
			}
			allowed[tc.ID] = true
			calls = append(calls, tc)
		}
		outputs := make([]FlatMessage, 0, len(calls))
		returned := map[string]bool{}
		for i+1 < len(msgs) && msgs[i+1].Role == "tool" {
			i++
			output := msgs[i]
			if allowed[output.ToolCallID] && !returned[output.ToolCallID] {
				returned[output.ToolCallID] = true
				outputs = append(outputs, output)
			}
		}
		m.ToolCalls = calls
		if len(calls) == 0 && strings.TrimSpace(m.Content) == "" {
			continue
		}
		result = append(result, m)
		result = append(result, outputs...)
		for _, tc := range calls {
			if !returned[tc.ID] {
				result = append(result, FlatMessage{Role: "tool", ToolCallID: tc.ID,
					Content: "[Tool result unavailable] No result was recorded for this call. It may have been interrupted; execution and side effects are unknown. Verify actual state before retrying. Do not treat this as success.",
					Extra:   map[string]any{"agentgo.tool_result_unavailable": true},
				})
			}
		}
	}
	return result
}

func flatFromSchema(msg *schema.Message) FlatMessage {
	fm := FlatMessage{
		Source:  msg,
		Extra:   msg.Extra,
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

func validSnippetHead(s string, n int) string {
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
func validSnippetTail(s string, n int) string {
	start := len(s) - n
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
