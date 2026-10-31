package core

import (
	"strings"
	"unicode"
)

// Slash commands that stand for a longer prompt.
//
// The composer sends what the user typed ("/review 并发安全"); the chat entry
// point calls ExpandCommand so the model gets the full instructions. Doing it
// once, on the backend, makes the web UI, the desktop shell, the terminal UI
// and ACP clients behave the same, and keeps the long text out of the composer.

const (
	commandBlockOpen  = "<command-instructions"
	commandBlockClose = "</command-instructions>"
)

// ExpandCommand expands a leading slash command in userText. The line the user
// typed stays first, so a transcript still reads "/review 并发安全"; the
// instructions follow in a <command-instructions> block that the UI folds.
// Anything that is not a known command is returned unchanged, as is text that
// already carries an expansion.
func ExpandCommand(userText string) string {
	text := strings.TrimSpace(userText)
	if !strings.HasPrefix(text, "/") || strings.Contains(text, commandBlockOpen) {
		return userText
	}
	name, arg := text, ""
	if i := strings.IndexFunc(text, unicode.IsSpace); i >= 0 {
		name, arg = text[:i], strings.TrimSpace(text[i:])
	}
	switch strings.ToLower(name) {
	case "/review":
		return text + "\n\n" + commandBlockOpen + ` name="review">` + "\n" + ReviewPrompt(arg) + "\n" + commandBlockClose
	case "/undo":
		return text + "\n\n" + commandBlockOpen + ` name="undo">` + "\n" + UndoPrompt(arg) + "\n" + commandBlockClose
	}
	return userText
}

// UndoPrompt is the instruction set of /undo. The journal and the tools that read
// it live in the file tools; this only tells the model how to use them for one
// request, and not to do anything else.
func UndoPrompt(arg string) string {
	var b strings.Builder
	b.WriteString("用户用 /undo 命令要求撤销本会话里智能体对文件做的修改。\n")
	b.WriteString("做法：\n")
	b.WriteString("1. 先调用 code_file_changes（如果工具列表里没有它，先用 tool_search 搜 file_changes），列出本会话记录的改动。没有记录就如实告诉用户，然后结束。\n")
	switch arg = strings.TrimSpace(arg); arg {
	case "":
		b.WriteString("2. 用户没有指定范围：调用 code_revert_file_changes，call_id 留空，只撤销最近一次工具调用做的改动。\n")
	case "all", "全部":
		b.WriteString("2. 用户要撤销整个会话的改动：调用 code_revert_file_changes，call_id 填 \"all\"。\n")
	default:
		b.WriteString("2. 用户给的范围是：" + arg + "。把它对应到 code_file_changes 列出的 call_id（可以是 call_id 本身，或按文件名、顺序描述的某一步），调用 code_revert_file_changes 撤销从该步开始的所有改动；对应不上就把列表给用户，问清楚，不要猜。\n")
	}
	b.WriteString("3. 只做撤销：不要顺手重新实现、修复或再次修改任何文件，也不要为了“撤销”而自己用 write/edit 去改文件。\n")
	b.WriteString("4. 把工具返回的结果如实告诉用户：哪些文件恢复了，哪些文件是智能体新建的、已删除，哪些因为用户之后又改过而没有动（这类要特别说明：没有覆盖用户自己的修改）。\n")
	return b.String()
}

// ReviewPrompt is the instruction set of /review: how to read the working-tree
// changes, what is worth reporting, how to grade it and how to answer. focus is
// the optional text the user typed after the command.
func ReviewPrompt(focus string) string {
	var b strings.Builder
	b.WriteString("用户用 /review 命令请求代码审查：审查当前工作区里尚未提交的改动（已暂存和未暂存的都算）。\n")
	if focus = strings.TrimSpace(focus); focus != "" {
		b.WriteString("审查重点（优先看这些，但不排斥发现其他问题）：" + focus + "\n")
	}
	b.WriteString(reviewRubric)
	return b.String()
}

const reviewRubric = `
做法
1. 先调用 get_workspace_changes，取得 git 状态、改动统计和 diff。diff 被截断时，再对每个改动文件分别带 path 参数调用。状态里以 ?? 开头的是新建文件：它们的内容已经带行号放在返回值的 untracked 字段里，不用再去读；个别没有完整给出的（太长、二进制、名字像密钥）才用读文件工具读。如果当前目录不是 git 仓库，或者没有任何改动，如实告诉用户然后结束，不要转去审查别的代码。
2. 只看 diff 容易误判：对每处改动，读一读它的调用方、被调用方和相关测试，确认确实有问题再报告。
3. 这次只审查：不要修改任何文件，也不要运行会改动工作区的命令；需要核实时，可以运行只读命令（例如已有的测试）。

什么值得报告（下面几条要同时满足）
- 问题是这次改动引入的：改动之前就存在的问题不报，即使它就在被改动的函数里，或者这次只是改名、调格式时碰到了它；只有这次改动让它更容易触发或扩大了影响，才报，并写明为什么。
- 它会实际影响正确性、安全、性能或可维护性，作者知道后会愿意修。
- 具体、可定位、可操作，不是泛泛的“建议加强”。
- 不依赖对作者意图的臆测；说“可能影响别处”时，要指出确切受影响的代码。
纯风格、格式、拼写问题不报，除非违反了项目文档（如 AGENTS.md）里的明确约定。没有值得报告的问题就如实说没有，不要凑数。

严重程度（按实际影响评级，不要夸大）
P0 必须马上修：会造成发布、数据或安全事故，且不依赖特殊输入。
P1 紧急：下个迭代前要修。
P2 一般：迟早要修。
P3 小改进：可修可不修。

输出格式（简体中文）
## 结论
一句话：这批改动能不能合入（可以合入 / 先修完再合入），以及最关键的原因。
## 问题
按严重程度从高到低排列，每条一个标题行加一段说明：
[P1] 标题 — 路径:起始行-结束行
说明：什么场景下会出问题、为什么，建议怎么改。
行号指改动后的文件，范围尽量短（不超过 10 行）。没有问题就写“未发现需要修复的问题”。
## 没能核实的部分
没来得及或没办法确认的点（例如没有运行测试）。没有就省略这一节。`
