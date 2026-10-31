package agent

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/cloudwego/eino/adk"

	"agentgo/internal/governance"
)

// Ungrounded tool-failure claims (audit-only).
//
// Round 4 found that some models end a run with a report that quotes a tool
// error ("sb_list 返回「未知工具」") which no tool of that run ever returned:
// deepseek-v4-flash did it in 14/99 tool-using baseline runs, and in 8 of those
// the task had in fact succeeded on disk. AgentGo's own tool errors never
// contain these phrases, so a final answer repeating one while no tool output
// of the same run contains it cannot be supported by the audit trail.
//
// The check only writes an audit row. It never blocks, rewrites or delays the
// answer: the final text is not evidence either way, and a quoted retraction
// ("上一轮我说未知工具是误判") is left alone by a sentence-level marker list.
// The matching is a text heuristic, so the row says "flagged", not "proved".

// toolFailureClaimPhrases are lower-case tool-error markers a model may quote.
// A phrase also counts as grounded when any tool output of the run contains it,
// e.g. eino's "tool X not found in toolsNode indexes".
var toolFailureClaimPhrases = []string{
	"未知工具", "unknown tool", "unknown function", "no such tool", "tool not found", "not found in toolsnode",
}

// claimRetractionStrong marks a whole answer as a correction of an earlier
// claim ("之前说工具不可用是误判") rather than a new claim about the current run.
// A bare "误" is deliberately not a marker: it would swallow "返回错误".
var claimRetractionStrong = []string{"更正", "不成立", "误判", "误报", "错的", "作废", "已可用", "可用了"}

// claimRetractionSentence marks single sentences that talk about an earlier
// turn or take a claim back.
var claimRetractionSentence = []string{
	"不对", "撤回", "修正", "并非", "不是真的", "现已", "已恢复", "那是", "当时", "上一轮", "上一条", "上轮",
	"之前", "此前", "先前", "前面", "早先", "刚才那",
}

var claimSentenceSplit = regexp.MustCompile(`[。！？!?；;\n]`)

// ungroundedToolClaim is one sentence of a final answer that quotes a tool
// failure no tool output of the run supports.
type ungroundedToolClaim struct {
	Phrase  string
	Excerpt string
}

// findUngroundedToolFailureClaims scans text for quoted tool failures. seen
// reports whether the run's tool outputs contained the phrase (nil: none did).
func findUngroundedToolFailureClaims(text string, seen func(phrase string) bool) []ungroundedToolClaim {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if armorContainsAny(strings.ToLower(text), claimRetractionStrong) {
		return nil // the answer corrects an earlier claim; precision first
	}
	var out []ungroundedToolClaim
	reported := map[string]bool{}
	for _, sentence := range claimSentenceSplit.Split(text, -1) {
		lower := strings.ToLower(sentence)
		for _, phrase := range toolFailureClaimPhrases {
			if !strings.Contains(lower, phrase) || reported[phrase] {
				continue
			}
			if seen != nil && seen(phrase) {
				continue
			}
			if armorContainsAny(lower, claimRetractionSentence) {
				continue
			}
			reported[phrase] = true
			out = append(out, ungroundedToolClaim{Phrase: phrase, Excerpt: clipRunes(strings.TrimSpace(sentence), 160)})
		}
	}
	return out
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// toolOutputPhrases remembers which claim phrases appeared in tool outputs of
// one run (tool errors reach the model as "[tool error] ..." tool messages).
type toolOutputPhrases struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (p *toolOutputPhrases) note(output string) {
	if output == "" {
		return
	}
	lower := strings.ToLower(output)
	for _, phrase := range toolFailureClaimPhrases {
		if strings.Contains(lower, phrase) {
			p.mu.Lock()
			if p.seen == nil {
				p.seen = map[string]bool{}
			}
			p.seen[phrase] = true
			p.mu.Unlock()
		}
	}
}

func (p *toolOutputPhrases) saw(phrase string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[phrase]
}

// maxToolOutputScan bounds how much of one streaming tool result is read.
const maxToolOutputScan = 64 << 10

// toolMessageText returns the text of a tool-role message variant. A streaming
// result is read (at most maxToolOutputScan bytes) and always closed. Tool
// results are never part of the assistant answer; this only feeds the claim check.
func toolMessageText(mv *adk.MessageVariant) string {
	if mv == nil {
		return ""
	}
	if mv.IsStreaming && mv.MessageStream != nil {
		defer mv.MessageStream.Close()
		var b strings.Builder
		for b.Len() < maxToolOutputScan {
			chunk, err := mv.MessageStream.Recv()
			if err != nil {
				break
			}
			if chunk != nil {
				b.WriteString(chunk.Content)
			}
		}
		return b.String()
	}
	if mv.Message != nil {
		return mv.Message.Content
	}
	return ""
}

// noteToolOutput records tool output text on the run's tracker, if any.
func noteToolOutput(ctx context.Context, output string) {
	if tr, ok := ctx.Value(toolUsageCtxKey{}).(*toolUsageTracker); ok && tr != nil {
		tr.outputs.note(output)
	}
}

// auditUngroundedToolFailureClaims writes one audit row per flagged final answer.
// Audit-only: the answer is returned unchanged by the caller.
func (r *Runner) auditUngroundedToolFailureClaims(ctx context.Context, sessionID, content string, tr *toolUsageTracker) {
	if r == nil || r.queue == nil || tr == nil {
		return
	}
	claims := findUngroundedToolFailureClaims(content, tr.outputs.saw)
	if len(claims) == 0 {
		return
	}
	phrases := make([]string, 0, len(claims))
	excerpts := make([]string, 0, len(claims))
	for _, c := range claims {
		phrases = append(phrases, c.Phrase)
		excerpts = append(excerpts, c.Excerpt)
	}
	args, _ := json.Marshal(map[string]any{
		"phrases":           phrases,
		"excerpts":          excerpts,
		"tools_used_in_run": tr.used.Load(),
	})
	_ = r.queue.RecordAudit(ctx, governance.AuditEntry{
		Channel: "governance", SessionID: strings.TrimPrefix(sessionID, "agentic:"),
		Action: "ungrounded_tool_failure_claim", Arguments: string(args), Result: "flagged", RiskLevel: "LOW",
		Explanation: "最终回答声称工具返回了错误（见 arguments.excerpts），但本轮没有任何工具输出包含该错误。" +
			"仅审计提示：不拦截、不改写回答；最终回答不能当作工具实际结果的证据，请以 tool_invoke 审计和磁盘 / 系统状态为准。",
	})
}
