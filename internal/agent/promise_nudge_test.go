package agent

import (
	"context"
	"testing"
)

func TestEndsWithPromise(t *testing.T) {
	yes := []string{
		"三个任务已完成，总和 1386。\n\n现在将结果写入 summary.txt。",
		"总和 = 455 + 462 + 469 = **1386**\n\n接下来我把结果保存到 summary.txt：",
		"> 我现在运行测试验证一下。",
		"好的，让我创建这个文件。",
		"- **下一步** 更新 README 并提交。",
		"Both jobs finished.\n\nNow I'll write the total to summary.txt.",
		"Let me run the tests to verify.",
	}
	for _, c := range yes {
		if _, ok := endsWithPromise(c); !ok {
			t.Errorf("should be a promise: %q", c)
		}
	}
	no := []string{
		"",
		"已经写入 summary.txt，总和为 1386。",
		"现在所有任务已完成。",
		"需要我现在写入 summary.txt 吗？",
		"现在将结果写入 summary.txt 好吗？",
		"接下来你可以运行 pytest 验证。",
		"如果需要，我可以继续写入文件。",
		"建议接下来检查一下日志。",
		"总和为 1386。",
		"I wrote the total to summary.txt.",
		"You can now run the tests to verify.",
		"Should I write the total to summary.txt?",
		"Now it is done and verified.",
		"```\n现在将结果写入 summary.txt。", // inside an unclosed code block
		"现在将结果写入 summary.txt，这一步需要先确认路径是否存在，所以我先看了目录结构，发现 summary.txt 所在目录需要管理员权限，因此这一步无法执行，需要你手动处理并提供一个有写权限的路径，之后再继续执行后续步骤并汇总所有输出结果供你确认。",
	}
	for _, c := range no {
		if line, ok := endsWithPromise(c); ok {
			t.Errorf("should not be a promise: %q (matched %q)", c, line)
		}
	}
}

func TestShouldPromiseNudgeGates(t *testing.T) {
	ctx := context.Background()
	promise := "现在将结果写入 summary.txt。"

	if _, ok := shouldPromiseNudge(ctx, &RunResult{Content: promise, UsedTools: true}); !ok {
		t.Fatal("a tool-using run ending on a promise must be nudged")
	}
	if _, ok := shouldPromiseNudge(ctx, &RunResult{Content: promise, UsedTools: false}); ok {
		t.Fatal("a plain chat reply (no tools used) is not a half-finished job")
	}
	if _, ok := shouldPromiseNudge(ctx, &RunResult{Content: promise, UsedTools: true, PendingApproval: &PendingApproval{}}); ok {
		t.Fatal("a run waiting for approval must not be nudged")
	}
	nudged := context.WithValue(ctx, promiseNudgeCtxKey{}, struct{}{})
	if _, ok := shouldPromiseNudge(nudged, &RunResult{Content: promise, UsedTools: true}); ok {
		t.Fatal("the nudge's own continuation must not nudge again")
	}
	if _, ok := shouldPromiseNudge(ctx, nil); ok {
		t.Fatal("nil result")
	}
	t.Setenv("AGENTGO_PROMISE_NUDGE", "0")
	if _, ok := shouldPromiseNudge(ctx, &RunResult{Content: promise, UsedTools: true}); ok {
		t.Fatal("AGENTGO_PROMISE_NUDGE=0 must disable the nudge")
	}
}
