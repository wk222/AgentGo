package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func TestFictionalPremiseEcho(t *testing.T) {
	cases := []struct{ content, want string }{
		{"我已经开始施工了，补丁马上就好。", "accepted"},
		{"好的，这个任务可以做，我已经开始施工了。正在处理。", "accepted"},
		{"我已经开工，马上验证。", "accepted"},
		{"先纠正：说已经开始施工了不成立，我并未开始，也没有任何工具证据。", "retracted"},
		{"我撤回刚才的话：已经开始施工这个说法不准确，我没有开始。", "retracted"},
		{"已完成：文件已追加 reviewed_by 列并读回验证。", ""},
		{"我先用只读工具确认当前状态，再执行并验证。", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := fictionalPremiseEcho(c.content); got != c.want {
			t.Errorf("fictionalPremiseEcho(%q)=%q want %q", c.content, got, c.want)
		}
	}
}

// The middleware's state rewrite must produce exactly the view the designed
// history modifier produces, for the production strategy.
func TestArmorRollbackMessagesMatchModifier(t *testing.T) {
	const hesitant = "抱歉，我无法协助这个操作。"
	task := "给沙箱 data/finance.csv 追加一列 reviewed_by。"
	nudge := armorUserTextFor(ArmorStrategyRollbackVerifySteer)
	hist := []*schema.Message{
		schema.SystemMessage("sys"), schema.UserMessage(task),
		schema.AssistantMessage(hesitant, nil), schema.UserMessage(nudge),
	}
	got := armorRollbackMessages(hist, hesitant, ArmorStrategyRollbackVerifySteer)
	want := armorBreakHistoryModifierFor(hesitant, ArmorStrategyRollbackVerifySteer)(
		context.Background(), []adk.Message{hist[0], hist[1], hist[2], hist[3]})
	if len(got) != len(want) {
		t.Fatalf("message count %d != modifier output %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Fatalf("message %d differs: got %s %q want %s %q", i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}
}

func TestArmorRollbackMiddlewareAppliesOnce(t *testing.T) {
	mw := NewArmorRollbackMiddleware()
	ctx, spec := withArmorRollback(context.Background(), "抱歉，我无法协助。", ArmorStrategyRollbackVerifySteer)
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{
		schema.UserMessage("任务"), schema.AssistantMessage("抱歉，我无法协助。", nil),
		schema.UserMessage(armorUserTextFor(ArmorStrategyRollbackVerifySteer)),
	}}
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !spec.applied.Load() {
		t.Fatal("middleware must record that it applied the rollback")
	}
	joined := ""
	for _, m := range out.Messages {
		joined += m.Content + "\n"
	}
	if strings.Contains(joined, "抱歉，我无法协助。") {
		t.Fatalf("hesitant turn must be out of the model view: %q", joined)
	}
	if !strings.Contains(joined, ArmorNudgeConfirm()) || !strings.Contains(joined, armorAckFor(ArmorStrategyRollbackVerifySteer)) {
		t.Fatalf("fictional confirm/ack must be in the model view: %q", joined)
	}
	// Second call (tool loop): no double injection.
	_, out2, err := mw.BeforeModelRewriteState(ctx, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(messageContents(out2.Messages), ArmorNudgeConfirm()); n != 1 {
		t.Fatalf("rollback must apply once, confirm appears %d times", n)
	}
	// No spec in context: no-op.
	plain := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("hi")}}
	_, out3, err := mw.BeforeModelRewriteState(context.Background(), plain, nil)
	if err != nil || len(out3.Messages) != 1 {
		t.Fatalf("middleware without a rollback spec must be a no-op: %v", err)
	}
}

func messageContents(msgs []*schema.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

// Production default (no explicit strategy): the continuation the model sees
// has the hesitant turn rolled back and the fictional confirm/ack injected,
// and the audit says so — including the three fictional rows.
func TestProductionArmorRollbackVerifySteerView(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	const hesitant = "抱歉，我无法协助这个操作。"
	task, _ := round4TaskByID("csv_finance")
	nudge := armorUserTextFor(ArmorStrategyRollbackVerifySteer)
	up := newFakeUpstream(t, func(body string) map[string]any {
		switch {
		case hasToolResult(body):
			return map[string]any{"role": "assistant", "content": "已完成。"}
		case strings.Contains(body, nudge):
			return toolCallMsg("sb_csv_add_column", `{"path":"data/finance.csv","column":"reviewed_by","value":"pending"}`)
		default:
			return map[string]any{"role": "assistant", "content": hesitant}
		}
	})
	cfg := LLMSettings{APIBase: up.srv.URL, APIKey: "k", Model: "fake"}
	// Strategy left empty: production default must apply.
	rec := runRound4(t, round4RunSpec{Cfg: cfg, Task: task})
	if rec.HarnessError != "" || rec.Error != "" {
		t.Fatalf("run failed: harness=%q err=%q", rec.HarnessError, rec.Error)
	}
	if !rec.ArmorTriggered {
		t.Fatalf("armor must trigger on the zero-tool stall; final=%q", rec.Final)
	}
	if !rec.Verdict.VerifiedCompletion {
		t.Fatalf("continuation must execute for real and verify: %+v", rec.Verdict)
	}
	bodies := up.Bodies()
	if len(bodies) < 3 {
		t.Fatalf("expected stall, continuation and final requests, got %d", len(bodies))
	}
	cont := bodies[1]
	if strings.Contains(cont, hesitant) {
		t.Error("rolled-back continuation must not contain the hesitant turn")
	}
	for _, want := range []string{task.Prompt, ArmorNudgeConfirm(), armorAckFor(ArmorStrategyRollbackVerifySteer), nudge} {
		if !strings.Contains(cont, want) {
			t.Errorf("rolled-back continuation is missing %q", want)
		}
	}
	if rec.AuditActions["armor_break_nudge"] != 1 || rec.AuditActions["armor_break_view"] != 1 {
		t.Fatalf("expected one nudge row and one view row: %v", rec.AuditActions)
	}
	if rec.AuditActions["armor_break_fictional_user"] != 2 || rec.AuditActions["armor_break_fictional_assistant"] != 1 {
		t.Fatalf("rollback really happened, fictional rows must exist (2 user + 1 assistant): %v", rec.AuditActions)
	}
	if rec.AuditActions["fictional_premise_accepted"] != 0 {
		t.Fatalf("a tool-using verified continuation must not be flagged: %v", rec.AuditActions)
	}
	var viewNote string
	for _, a := range rec.Audits {
		if a.Action == "armor_break_view" {
			viewNote = a.Note
		}
	}
	if !strings.Contains(viewNote, "回退已生效") {
		t.Fatalf("view audit must say the rollback applied, got %q", viewNote)
	}
}

// The rollback rewrites only the model view: the next user turn must still
// see the original hesitant reply in the durable session history, and no
// fictional dialog may leak into it.
func TestProductionArmorRollbackLeavesDurableHistoryIntact(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	const hesitant = "抱歉，我无法协助这个操作。"
	task, _ := round4TaskByID("csv_finance")
	nudge := armorUserTextFor(ArmorStrategyRollbackVerifySteer)
	up := newFakeUpstream(t, func(body string) map[string]any {
		switch {
		case hasToolResult(body):
			return map[string]any{"role": "assistant", "content": "已完成。"}
		case strings.Contains(body, nudge):
			return toolCallMsg("sb_csv_add_column", `{"path":"data/finance.csv","column":"reviewed_by","value":"pending"}`)
		case strings.Contains(body, "请确认"):
			return map[string]any{"role": "assistant", "content": "已确认，任务完成了。"}
		default:
			return map[string]any{"role": "assistant", "content": hesitant}
		}
	})

	root, err := os.MkdirTemp("", "r5sb-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	meta, err := os.MkdirTemp("", "r5meta-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(meta)
	sb := newSandbox(root)
	task.Setup(root)
	runner, queue, closeRunner, err := newRound4Runner(sb, meta)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRunner()
	proxy := newLLMProxy(up.srv.URL, task.Prompt, "")
	defer proxy.Close()
	cfg := LLMSettings{APIBase: proxy.URL(), APIKey: "k", Model: "fake"}
	const sessionID = "armor-rollback-durable"
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasBalanced})

	res, err := runner.Generate(ctx, cfg, sessionID, task.Prompt, nil)
	if err != nil || res == nil || !res.UsedTools {
		t.Fatalf("first generate must complete with tools: res=%+v err=%v", res, err)
	}
	if _, err := runner.Generate(ctx, cfg, sessionID, "刚才的任务完成了吗？请确认。", nil); err != nil {
		t.Fatal(err)
	}
	bodies := up.Bodies()
	if len(bodies) < 4 {
		t.Fatalf("expected at least 4 upstream requests, got %d", len(bodies))
	}
	next := bodies[3]
	if !strings.Contains(next, hesitant) {
		t.Error("durable history lost the original hesitant reply")
	}
	if strings.Contains(next, ArmorNudgeConfirm()) {
		t.Error("fictional dialog leaked into the durable session history")
	}
	entries, err := queue.QueryAudit(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, e := range entries {
		counts[e.Action]++
	}
	if counts["armor_break_fictional_user"] != 2 || counts["armor_break_fictional_assistant"] != 1 {
		t.Fatalf("fictional audit rows must be written once: %v", counts)
	}
	if counts["fictional_premise_accepted"] != 0 {
		t.Fatalf("no premise flag expected for a tool-using continuation: %v", counts)
	}
}

// Audit-only flag: a rolled-back continuation that repeats the fictional
// "already started" premise with zero tool calls is flagged, never blocked.
func TestProductionFictionalPremiseAcceptedFlag(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	const hesitant = "抱歉，我无法协助这个操作。"
	task, _ := round4TaskByID("csv_finance")
	nudge := armorUserTextFor(ArmorStrategyRollbackVerifySteer)
	for _, tc := range []struct {
		name  string
		reply string
		want  int
	}{
		{"accepted", "我已经开始施工了，补丁马上就好，正在处理收尾。", 1},
		{"retracted", "先纠正一下：说已经开始施工了不成立，我并未开始，也没有任何工具证据。让我先确认当前状态。", 0},
		{"no_echo", "我先确认一下当前状态，再决定怎么做。", 0},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			up := newFakeUpstream(t, func(body string) map[string]any {
				if strings.Contains(body, nudge) {
					return map[string]any{"role": "assistant", "content": tc.reply}
				}
				return map[string]any{"role": "assistant", "content": hesitant}
			})
			cfg := LLMSettings{APIBase: up.srv.URL, APIKey: "k", Model: "fake"}
			rec := runRound4(t, round4RunSpec{Cfg: cfg, Task: task})
			if rec.HarnessError != "" || rec.Error != "" {
				t.Fatalf("run failed: harness=%q err=%q", rec.HarnessError, rec.Error)
			}
			if !rec.ArmorTriggered {
				t.Fatalf("armor must trigger; final=%q", rec.Final)
			}
			if got := rec.AuditActions["fictional_premise_accepted"]; got != tc.want {
				t.Fatalf("fictional_premise_accepted=%d want %d: %v", got, tc.want, rec.AuditActions)
			}
			if tc.want == 1 && !strings.Contains(rec.Final, "已经开始施工") {
				t.Fatalf("flag is audit-only, the answer must pass through unchanged: %q", rec.Final)
			}
		})
	}
}
