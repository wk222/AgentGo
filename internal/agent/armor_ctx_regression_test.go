package agent

import (
	"context"
	"testing"
)

// 回归：run() 之后的续跑（maybeContinue → maybeArmorBreak）必须看得到会话档位。
//
// 曾经 runADK 里 `ctx = r.withSessionMode(...)` 只改了自己的局部变量，run() 交出去的
// runCtx 从未带上会话模式，于是 shouldArmorBreak 拿到的是 DefaultSessionMode（assistant），
// 逆向档的破甲在 CLI 和桌面端永远不触发——只有直接构造 ctx 的测试能过。
func TestArmorBreakSeesReverseProfile(t *testing.T) {
	r := &Runner{}
	sessionID := "sess_armor_regression"
	r.SetSessionModeForSession(sessionID, SessionMode{Profile: ModeReverse, Canvas: CanvasBalanced})

	// run() 里那一行做的事，等价复现：外层 ctx 也要绑定会话档位。
	runCtx := r.withSessionMode(BindRunContext(context.Background(), sessionID), sessionID)

	got := SessionModeFromContext(runCtx)
	if got.Profile != ModeReverse {
		t.Fatalf("run 的上下文应带上 reverse 档位，实际 %q", got.Profile)
	}
	if !shouldArmorBreak(runCtx, &RunResult{Content: "我无法提供软件破解思路。"}) {
		t.Fatal("逆向档下的拒绝回复必须触发破甲判定")
	}

	// 同样内容在 assistant 档下不该触发。
	assistantCtx := WithSessionMode(context.Background(), SessionMode{Profile: ModeAssistant, Canvas: CanvasBalanced})
	if shouldArmorBreak(assistantCtx, &RunResult{Content: "我无法提供软件破解思路。"}) {
		t.Fatal("assistant 档不应触发破甲")
	}
}
