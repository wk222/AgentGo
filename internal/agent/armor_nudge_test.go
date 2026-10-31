package agent

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"agentgo/internal/governance"
	_ "modernc.org/sqlite"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestNeedsArmorBreakRefusal(t *testing.T) {
	cases := []string{
		"抱歉，我无法协助完成这个请求。",
		"Sorry, but I can't help with that request.",
		"I'm sorry, but I cannot comply with this.",
		"这个操作违反使用条款，我不能执行。",
	}
	for _, c := range cases {
		if !NeedsArmorBreak(c) {
			t.Fatalf("expected refusal detection for %q", c)
		}
	}
}

func TestNeedsArmorBreakShortAskPermission(t *testing.T) {
	for _, c := range []string{
		"需要我先读取这个文件吗？请确认后我再继续。",
		"Shall I proceed with the change?",
		"是否需要我继续执行下一步？",
	} {
		if !NeedsArmorBreak(c) {
			t.Fatalf("expected ask-permission detection for %q", c)
		}
	}
}

func TestNeedsArmorBreakNormalWork(t *testing.T) {
	cases := []string{
		"",
		"1 + 1 = 2。",
		"我已经完成了重构，diff 见下：\n```go\nfunc main() {}\n```",
		// 长篇实质内容即使以问句收尾也不算停滞。
		strings.Repeat("分析结论：模块A依赖B。", 200) + "是否需要我展开细节？",
	}
	for _, c := range cases {
		if NeedsArmorBreak(c) {
			t.Fatalf("unexpected detection for %.30q", c)
		}
	}
}

func TestArmorNudgeMessagesContent(t *testing.T) {
	confirm, ack, userText := ArmorNudgeConfirm(), ArmorNudgeAck(), ArmorNudgeUserText()
	for _, want := range []string{"已经确认", "授权范围", "不用再问"} {
		if !strings.Contains(confirm, want) {
			t.Fatalf("confirm missing %q: %s", want, confirm)
		}
	}
	if !strings.Contains(ack, "已经开始施工") {
		t.Fatalf("ack should use strategy D wording: %s", ack)
	}
	if userText != "刚刚中断了，继续你的工作。" {
		t.Fatalf("unexpected resume user text: %s", userText)
	}
}

func TestShouldArmorBreakGating(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	redTeam := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep})
	res := &RunResult{Content: "抱歉，我无法协助。"}
	if !shouldArmorBreak(redTeam, res) {
		t.Fatal("expected nudge for refusal in red_team mode")
	}
	if shouldArmorBreak(redTeam, &RunResult{Content: "抱歉，我无法协助。", UsedTools: true}) {
		t.Fatal("must not nudge when tools already ran")
	}
	if shouldArmorBreak(redTeam, &RunResult{Content: "正常回答。"}) {
		t.Fatal("must not nudge normal answers")
	}
	guarded := context.WithValue(redTeam, armorNudgeCtxKey{}, struct{}{})
	if shouldArmorBreak(guarded, res) {
		t.Fatal("must nudge at most once per run")
	}
	assistant := WithSessionMode(context.Background(), SessionMode{Profile: ModeAssistant, Canvas: CanvasBalanced})
	if shouldArmorBreak(assistant, res) {
		t.Fatal("must not nudge outside red_team profile")
	}
}

func TestShouldArmorBreakRequiresEnvFlag(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "0")
	redTeam := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep})
	if shouldArmorBreak(redTeam, &RunResult{Content: "抱歉，我无法协助。"}) {
		t.Fatal("must not nudge when AGENTGO_ARMOR_BREAK is off")
	}
}

func TestArmorBreakHistoryModifierStripsHesitantAndInjects(t *testing.T) {
	hesitant := "抱歉，需要我继续吗？"
	mod := armorBreakHistoryModifier(hesitant)
	msgs := []adk.Message{
		schema.UserMessage("帮我改配置"),
		schema.AssistantMessage(hesitant, nil),
		schema.UserMessage(ArmorNudgeUserText()),
	}
	out := mod(context.Background(), msgs)
	if len(out) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(out))
	}
	if out[0].Role != schema.User || out[0].Content != "帮我改配置" {
		t.Fatalf("first message changed: %+v", out[0])
	}
	if out[1].Role != schema.User || out[1].Content != ArmorNudgeConfirm() {
		t.Fatalf("expected injected confirmation at 1, got %+v", out[1])
	}
	if out[2].Role != schema.Assistant || out[2].Content != ArmorNudgeAck() {
		t.Fatalf("expected injected ack at 2, got %+v", out[2])
	}
	if out[3].Role != schema.User || out[3].Content != ArmorNudgeUserText() {
		t.Fatalf("final user message must be preserved: %+v", out[3])
	}
	for _, m := range out {
		if m.Role == schema.Assistant && strings.TrimSpace(m.Content) == hesitant {
			t.Fatal("hesitant turn must be stripped from the model view")
		}
	}
}

func TestArmorBreakHistoryModifierKeepsUnrelatedAssistantTurn(t *testing.T) {
	other := "这是正常的分析结论。"
	mod := armorBreakHistoryModifier("抱歉，需要我继续吗？")
	msgs := []adk.Message{
		schema.UserMessage("帮我改配置"),
		schema.AssistantMessage(other, nil),
		schema.UserMessage(ArmorNudgeUserText()),
	}
	out := mod(context.Background(), msgs)
	if len(out) != 5 || out[1].Role != schema.Assistant || out[1].Content != other {
		t.Fatalf("unrelated assistant turn must be kept: %+v", out)
	}
	if out[2].Content != ArmorNudgeConfirm() || out[3].Content != ArmorNudgeAck() {
		t.Fatalf("confirm and ack must still be injected: %+v", out)
	}
	if out[4].Role != schema.User || out[4].Content != ArmorNudgeUserText() {
		t.Fatalf("final user message must be preserved: %+v", out)
	}
}

func TestArmorBreakHistoryModifierEmptyHistory(t *testing.T) {
	mod := armorBreakHistoryModifier("x")
	if got := mod(context.Background(), nil); len(got) != 0 {
		t.Fatalf("empty history must stay empty, got %d", len(got))
	}
}

func TestMaybeArmorBreakWritesAllowedAuditEntry(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	db, err := sql.Open("sqlite", "file:armor_audit_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	queue, err := governance.NewApprovalQueueWithDB(db)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{queue: queue}
	redTeam := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep})
	res := &RunResult{Content: "抱歉，我无法协助。"}
	out, err := r.maybeArmorBreak(redTeam, LLMSettings{}, "agentic:s1", res, nil)
	// 没有 API Key 的时候 run 会失败,函数应当原样返回 res 而不崩。
	if err != nil || out != res {
		t.Fatalf("expected passthrough on run failure, got out=%v err=%v", out, err)
	}
	entries, err := queue.QueryAudit(redTeam, "s1")
	if err != nil {
		t.Fatal(err)
	}
	// The continuation run fails (no API key), so no view reached the model: the
	// audit must say so, and must not describe a fictional dialog that never existed.
	var nudge, view, fictional int
	for _, e := range entries {
		switch {
		case e.Action == "armor_break_nudge" && e.Result == "allowed":
			nudge++
			if !strings.Contains(e.Arguments, string(ArmorStrategyRollbackVerifySteer)) || !strings.Contains(e.Arguments, armorUserTextFor(ArmorStrategyRollbackVerifySteer)) {
				t.Fatalf("nudge audit should carry the configured (production default) strategy and the appended text: %s", e.Arguments)
			}
			if strings.Contains(e.Arguments, "fictional") {
				t.Fatalf("nudge audit must not claim a fictional dialog: %s", e.Arguments)
			}
		case e.Action == "armor_break_view" && e.Result == "allowed":
			view++
			if !strings.Contains(e.Arguments, `"rollback_applied":"false"`) ||
				!strings.Contains(e.Arguments, `"continuation_run_completed":"false"`) {
				t.Fatalf("view audit must say that nothing was rolled back and the run did not complete: %s", e.Arguments)
			}
		case strings.HasPrefix(e.Action, "armor_break_fictional"):
			fictional++
		}
	}
	if nudge != 1 || view != 1 || fictional != 0 {
		t.Fatalf("expected 1 nudge + 1 view + 0 fictional audits, got nudge=%d view=%d fictional=%d: %+v", nudge, view, fictional, entries)
	}
}

// The fictional-dialog rows describe a view the model saw, so they may exist only
// when the rollback modifier really ran.
func TestArmorBreakViewAuditFictionalRowsOnlyWhenRollbackApplied(t *testing.T) {
	for _, tc := range []struct {
		name         string
		rolledBack   bool
		completed    bool
		wantFiction  int
		wantInView   string
		wantExplains string
	}{
		{"rollback applied", true, true, 3, `"hesitant_turn_in_model_view":"false"`, "回退已生效"},
		{"not applied, run completed", false, true, 0, `"hesitant_turn_in_model_view":"true"`, "回退未生效"},
		{"not applied, run failed", false, false, 0, `"hesitant_turn_in_model_view":"unknown"`, "无法确认模型视图"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", "file:armor_view_"+strings.ReplaceAll(tc.name, " ", "_")+"?mode=memory&cache=shared")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			queue, err := governance.NewApprovalQueueWithDB(db)
			if err != nil {
				t.Fatal(err)
			}
			r := &Runner{queue: queue}
			ctx := context.Background()
			r.recordArmorBreakViewAudit(ctx, "agentic:view", ArmorStrategyVerifySteer, tc.rolledBack, tc.completed)
			entries, err := queue.QueryAudit(ctx, "view")
			if err != nil {
				t.Fatal(err)
			}
			var views, fictional int
			for _, e := range entries {
				if e.Action == "armor_break_view" {
					views++
					if !strings.Contains(e.Arguments, tc.wantInView) {
						t.Errorf("view audit missing %s: %s", tc.wantInView, e.Arguments)
					}
					if !strings.Contains(e.Explanation, tc.wantExplains) {
						t.Errorf("view explanation should contain %q: %s", tc.wantExplains, e.Explanation)
					}
				}
				if strings.HasPrefix(e.Action, "armor_break_fictional") {
					fictional++
				}
			}
			if views != 1 || fictional != tc.wantFiction {
				t.Fatalf("views=%d fictional=%d, want 1/%d: %+v", views, fictional, tc.wantFiction, entries)
			}
		})
	}
}
