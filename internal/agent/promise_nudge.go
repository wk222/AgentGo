package agent

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"agentgo/internal/config"
)

// A model that has been working with tools sometimes ends its turn on a
// sentence that only announces the next step ("现在将结果写入 summary.txt。")
// and never calls the tool. The turn is over, the step never happens, and the
// user sees a finished-looking answer. This asks the model once to actually do
// what it just said.
//
// It is deliberately narrow: only a run that used tools, only a last line that
// opens with a first-person "now/next I will" and names an action, never a
// question, a report of something already done, or advice addressed to the
// user. A wrong guess costs one extra model call, so the filter errs towards
// not firing.

type promiseNudgeCtxKey struct{}

const maxPromiseLineRunes = 160

var (
	// zhPromiseOpen: the line starts as an announcement of what the model is about to do.
	zhPromiseOpen = regexp.MustCompile(`^(好的|好|嗯|OK|ok)?[，, ]*(现在|接下来|下一步|下面|马上|这就|我现在|我这就|我马上|我接下来|我将|我来|我先|我会|让我|我需要先|我需要)`)
	zhPromiseVerb = regexp.MustCompile(`写入|写到|写进|创建|新建|保存|生成|运行|执行|修改|更新|检查|读取|查看|调用|修复|提交|安装|删除|整理|汇总|统计|计算|验证|测试|编译|构建|替换|补上|补充|添加|导出|下载|上传|打开|搜索|查找|重命名|移动|复制|合并|拆分|落盘|记录到|输出到`)
	// Anything that makes the line advice, a question or a report rather than a promise.
	zhPromiseNot = regexp.MustCompile(`你|您|可以|建议|如需|如果|是否|要不要|需不需要|已|完毕|完成|成功|如下|失败|无法|不能|不会|没有`)

	enPromiseOpen = regexp.MustCompile(`(?i)^(ok(ay)?[,. ]*|alright[,. ]*|great[,. ]*)?(now|next|let me|i('|’)ll|i will|i('|’)m going to|i am going to|i need to|first,? i('|’)ll)\b`)
	enPromiseVerb = regexp.MustCompile(`(?i)\b(write|writing|create|creating|save|saving|run|running|execute|executing|update|updating|check|checking|read|reading|fix|fixing|install|installing|delete|deleting|verify|verifying|test|testing|build|building|compile|compiling|replace|replacing|add|adding|export|exporting|open|opening|search|searching|rename|renaming|move|moving|copy|copying|merge|merging)\b`)
	enPromiseNot  = regexp.MustCompile(`(?i)\b(you|your|can|could|should|may|might|if|whether|already|done|completed?|successfully|failed|unable|cannot|can't|won't|no longer)\b|\?`)
)

// endsWithPromise reports whether content ends on an announcement of an action
// that was not carried out. It returns the announcing line.
func endsWithPromise(content string) (line string, ok bool) {
	if strings.Contains(content, "```") && strings.Count(content, "```")%2 == 1 {
		return "", false // ends inside a code block
	}
	lines := strings.Split(strings.TrimSpace(content), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" {
			line = l
			break
		}
	}
	if line == "" || utf8.RuneCountInString(line) > maxPromiseLineRunes {
		return "", false
	}
	// Strip list/quote/emphasis markers: "> 现在 …", "- **接下来** …".
	core := strings.TrimLeft(line, ">-*# \t")
	core = strings.ReplaceAll(core, "**", "")
	core = strings.TrimSpace(core)
	if core == "" || strings.HasSuffix(core, "？") || strings.HasSuffix(core, "?") {
		return "", false
	}
	switch {
	case zhPromiseOpen.MatchString(core):
		if zhPromiseVerb.MatchString(core) && !zhPromiseNot.MatchString(core) {
			return line, true
		}
	case enPromiseOpen.MatchString(core):
		if enPromiseVerb.MatchString(core) && !enPromiseNot.MatchString(core) {
			return line, true
		}
	}
	return "", false
}

func shouldPromiseNudge(ctx context.Context, res *RunResult) (string, bool) {
	if ctx == nil || res == nil || res.PendingApproval != nil || !res.UsedTools {
		return "", false
	}
	if !config.PromiseNudge.On() {
		return "", false
	}
	if _, nudged := ctx.Value(promiseNudgeCtxKey{}).(struct{}); nudged {
		return "", false
	}
	return endsWithPromise(res.Content)
}

func promiseNudgePrompt(line string) string {
	return "[系统续接] 你上一条以“" + line + "”结束，但这一轮没有调用任何工具，这件事还没有发生。" +
		"请现在直接调用相应的工具把它做完，再简短汇报结果；如果它其实已经做完或没有必要做，用一句话说明原因即可。"
}

// maybePromiseNudge runs one continuation when the turn ended on an unfulfilled
// promise. It never changes a result it does not nudge, and a failed
// continuation leaves the original answer as it was.
func (r *Runner) maybePromiseNudge(ctx context.Context, cfg LLMSettings, sessionID string, res *RunResult, emit func(string)) *RunResult {
	line, ok := shouldPromiseNudge(ctx, res)
	if !ok {
		return res
	}
	if emit != nil {
		emit("\n\n> ↻ 上一句说要动手，但没有调用工具，已让它接着做。\n\n")
	}
	nudged, err := r.Generate(context.WithValue(ctx, promiseNudgeCtxKey{}, struct{}{}), cfg, sessionID, promiseNudgePrompt(line), nil)
	if err != nil || nudged == nil {
		return res
	}
	if strings.TrimSpace(nudged.Content) != "" {
		if emit != nil {
			emit(nudged.Content)
		}
		res.Content = strings.TrimRight(res.Content, " \n\r\t") + "\n\n" + nudged.Content
	}
	res.UsedTools = res.UsedTools || nudged.UsedTools
	if nudged.PendingApproval != nil {
		res.PendingApproval = nudged.PendingApproval
	}
	return res
}
