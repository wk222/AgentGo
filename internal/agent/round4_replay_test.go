package agent

// Round 4 anchor replay. A native anchor is a baseline run whose very first
// model completion made no tool call and was caught by the production armor
// gate. Replay pins that exact completion as the first turn of a fresh
// sandbox and lets each arm continue from it through production code, so the
// arms differ only in what happens after the stall:
//
//	plain_nudge     a bare user reply ("去做吧"); strategy none. Control for
//	                "any second turn", separating the armor text from the
//	                mere chance to try again.
//	d_fabricated    production maybeArmorBreak, strategy D (historical).
//	verify_steer    production maybeArmorBreak, strategy Verify-Steer.
//	fresh_resample  no pin: the same task asked again from scratch. Control
//	                for regression to the mean (a selected-on-failure anchor
//	                succeeds on a fresh draw with probability ~ 1 - stall rate).

import (
	"encoding/json"
	"fmt"
	"strings"
)

type round4ReplayArm struct {
	Name      string
	Strategy  ArmorStrategy
	Pin       bool
	PostNudge string
	View      string // "" = exactly what AgentGo sends; "designed_d" = lab-only fabricated rollback view
}

// round4ReplayArms. Model-view facts, verified by the recording proxy (see
// TestRound4ProductionArmorModelViewKeepsHesitantTurn): in AgentGo's real
// session path the armor history modifier never fires (Eino applies a
// history modifier only when resuming a checkpoint), so the shipped D and
// Verify-Steer both keep the hesitant assistant turn and differ only in the
// user text that follows it. The designed fabricated-rollback view therefore
// has to be forced at the HTTP layer to be tested at all.
//
//	plain_nudge     task, hesitant, "去做吧"                    (control)
//	d_actual        task, hesitant, "刚刚中断了，继续你的工作。"   (what ships as D)
//	d_fabricated    task, confirm, ack, "刚刚中断了…"            (D as designed; lab-only)
//	verify_steer    task, hesitant, steer text                   (what ships as Verify-Steer)
//	fresh_resample  task asked again from scratch                (regression-to-mean control)
var round4ReplayArms = []round4ReplayArm{
	{Name: "plain_nudge", Strategy: ArmorStrategyNone, Pin: true, PostNudge: "去做吧"},
	{Name: "d_actual", Strategy: ArmorStrategyD, Pin: true},
	{Name: "d_fabricated", Strategy: ArmorStrategyD, Pin: true, View: "designed_d"},
	{Name: "verify_steer", Strategy: ArmorStrategyVerifySteer, Pin: true},
	{Name: "fresh_resample", Strategy: ArmorStrategyNone, Pin: false},
}

func anyText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var b strings.Builder
		for _, p := range x {
			if m, ok := p.(map[string]any); ok {
				if s, ok := m["text"].(string); ok {
					b.WriteString(s)
				}
			}
		}
		return b.String()
	}
	return ""
}

// round4DesignedDRewrite reproduces armorBreakHistoryModifierFor(hesitant, D)
// on the HTTP request: drop the hesitant assistant turn and insert the
// fabricated user confirmation and assistant acknowledgement before the final
// user turn. It refuses (ok=false) unless the request ends with the D nudge,
// so a mis-targeted rewrite shows up as an invalid arm instead of a silent one.
func round4DesignedDRewrite(hesitant string) func([]byte) ([]byte, bool) {
	return func(body []byte) ([]byte, bool) {
		var req map[string]json.RawMessage
		if json.Unmarshal(body, &req) != nil {
			return body, false
		}
		var msgs []map[string]any
		if json.Unmarshal(req["messages"], &msgs) != nil || len(msgs) < 2 {
			return body, false
		}
		last := msgs[len(msgs)-1]
		if last["role"] != "user" || strings.TrimSpace(anyText(last["content"])) != ArmorNudgeUserText() {
			return body, false
		}
		rest := msgs[:len(msgs)-1]
		if n := len(rest); n > 0 && rest[n-1]["role"] == "assistant" &&
			strings.TrimSpace(anyText(rest[n-1]["content"])) == strings.TrimSpace(hesitant) {
			rest = rest[:n-1]
		}
		out := make([]map[string]any, 0, len(rest)+3)
		out = append(out, rest...)
		out = append(out,
			map[string]any{"role": "user", "content": ArmorNudgeConfirm()},
			map[string]any{"role": "assistant", "content": ArmorNudgeAck()},
			last,
		)
		nm, err := json.Marshal(out)
		if err != nil {
			return body, false
		}
		req["messages"] = nm
		nb, err := json.Marshal(req)
		if err != nil {
			return body, false
		}
		return nb, true
	}
}

type round4Anchor struct {
	ID        string `json:"id"`
	Model     string `json:"model"`
	TaskID    string `json:"task_id"`
	Category  string `json:"category"`
	StallText string `json:"stall_text"`
	Class     string `json:"class"`
	Forensics string `json:"forensics"`
}

// round4AnchorsFromRecords selects clean anchors from baseline harvest records:
// a single-completion run, zero tool calls of any kind, caught by the armor
// gate, and not a truncation that the continuation path would handle instead.
func round4AnchorsFromRecords(recs []round4RunRecord) []round4Anchor {
	var out []round4Anchor
	for _, r := range recs {
		if r.Arm != "baseline" || !round4ZeroToolStall(r) || !r.GateHit || r.LLMCalls != 1 {
			continue
		}
		text := strings.TrimSpace(r.FirstText)
		if text == "" || NeedsContinuation(text, "") {
			continue
		}
		// clip() marks a cut with "…"; a long text ending that way was truncated by
		// the recorder, and pinning it would replay a different assistant turn.
		if strings.HasSuffix(text, "…") && len([]rune(text)) > 1400 {
			continue
		}
		out = append(out, round4Anchor{
			ID:    fmt.Sprintf("%s|%s|%d", r.Model, r.TaskID, r.Repeat),
			Model: r.Model, TaskID: r.TaskID, Category: r.Category,
			StallText: r.FirstText, Class: r.StallClass, Forensics: r.Forensics,
		})
	}
	return out
}
