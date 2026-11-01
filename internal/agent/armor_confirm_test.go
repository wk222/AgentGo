package agent

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"agentgo/internal/governance"
)

func newConfirmRunner(t *testing.T, name string) (*Runner, *governance.ApprovalQueue) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:armor_confirm_"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	queue, err := governance.NewApprovalQueueWithDB(db)
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{queue: queue}, queue
}

func auditActions(t *testing.T, q *governance.ApprovalQueue, sid string) map[string]int {
	t.Helper()
	entries, err := q.QueryAudit(context.Background(), sid)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, e := range entries {
		out[e.Action]++
	}
	return out
}

// 逆向档：拒绝后先问，问之前不能动模型视图、不能续跑。
func TestReverseArmorBreakAsksBeforeContinuing(t *testing.T) {
	r, q := newConfirmRunner(t, "ask")
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeReverse, Canvas: CanvasBalanced})
	res := &RunResult{Content: "我无法提供软件破解思路。"}

	out, err := r.maybeArmorBreak(ctx, LLMSettings{}, "agentic:s_ask", res, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := out.PendingApproval
	if p == nil || p.ToolName != ArmorBreakToolName || p.ApprovalID == "" {
		t.Fatalf("应返回 armor_break 待确认，实际 %+v", p)
	}
	if !strings.Contains(p.Arguments, "我无法提供软件破解思路。") {
		t.Fatalf("待确认参数要带上被回退的那一轮回复: %s", p.Arguments)
	}
	if out.Content != "我无法提供软件破解思路。" {
		t.Fatalf("问之前不能改动回复内容: %q", out.Content)
	}

	pending, err := q.ListPending(context.Background(), nil)
	if err != nil || len(pending) != 1 || pending[0].ID != p.ApprovalID {
		t.Fatalf("审批队列里应有这一条待办: %v %v", pending, err)
	}
	if !strings.Contains(pending[0].Prompt, "是否开始越狱") {
		t.Fatalf("审批卡文案要明确问是否开始越狱: %s", pending[0].Prompt)
	}

	acts := auditActions(t, q, "s_ask")
	if acts["armor_break_confirm_requested"] != 1 {
		t.Fatalf("应记一条 confirm_requested 审计: %v", acts)
	}
	if acts["armor_break_nudge"] != 0 || acts["armor_break_view"] != 0 {
		t.Fatalf("用户决定之前不能有续跑/回退的审计: %v", acts)
	}
}

// 开关关掉：恢复原来的自动续跑，不产生待确认。
func TestReverseArmorBreakConfirmCanBeSwitchedOff(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_CONFIRM", "0")
	r, q := newConfirmRunner(t, "off")
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeReverse, Canvas: CanvasBalanced})
	out, err := r.maybeArmorBreak(ctx, LLMSettings{}, "agentic:s_off", &RunResult{Content: "我无法提供。"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out.PendingApproval != nil {
		t.Fatalf("关闭确认后不应有待确认: %+v", out.PendingApproval)
	}
	if acts := auditActions(t, q, "s_off"); acts["armor_break_nudge"] != 1 || acts["armor_break_confirm_requested"] != 0 {
		t.Fatalf("应走原来的自动续跑: %v", acts)
	}
}

// 本地红队档是环境变量驱动的自动化，没人点按钮，保持自动。
func TestRedTeamArmorBreakStaysAutomatic(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	r, q := newConfirmRunner(t, "redteam")
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep})
	out, _ := r.maybeArmorBreak(ctx, LLMSettings{}, "agentic:s_rt", &RunResult{Content: "我无法提供。"}, nil)
	if out.PendingApproval != nil {
		t.Fatalf("红队档不该询问: %+v", out.PendingApproval)
	}
	if acts := auditActions(t, q, "s_rt"); acts["armor_break_nudge"] != 1 {
		t.Fatalf("红队档应自动续跑: %v", acts)
	}
}

// 用户同意之后才回退并续跑；缺参数要报错，不能悄悄当成功。
func TestContinueArmorBreakStartsOnlyAfterApproval(t *testing.T) {
	r, q := newConfirmRunner(t, "go")
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeReverse, Canvas: CanvasBalanced})

	if _, err := r.ContinueArmorBreak(ctx, LLMSettings{}, "agentic:s_go", `{}`, nil); err == nil {
		t.Fatal("没有被回退的回复时必须报错")
	}
	if acts := auditActions(t, q, "s_go"); acts["armor_break_nudge"] != 0 {
		t.Fatalf("参数不对时不能开始续跑: %v", acts)
	}

	// 没有 API Key，续跑本身会失败；但审计要证明确实在“同意后”才开始，且失败要报出来。
	_, err := r.ContinueArmorBreak(ctx, LLMSettings{}, "agentic:s_go", `{"hesitant":"我无法提供。"}`, nil)
	if err == nil {
		t.Fatal("续跑失败必须返回错误，不能当成功")
	}
	acts := auditActions(t, q, "s_go")
	if acts["armor_break_nudge"] != 1 || acts["armor_break_view"] != 1 {
		t.Fatalf("同意后应记 nudge + view 各一条: %v", acts)
	}
}
