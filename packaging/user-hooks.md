# 用户钩子（hooks.json）

钩子是你自己写的小脚本，AgentGo 在固定的几个时机自动运行它：智能体要调用工具之前、之后，你发出消息之后，智能体答完一轮之后。脚本可以**拦下**这一步（并告诉模型和你原因）、**记录**发生了什么、或**补充**一段上下文。

适合的场景：公司规定"不许删 build 目录""不许碰某个共享盘"；每次答完发一条通知；把每次工具调用记进审计文件；给每条消息自动带上"今天的会议室是 4417"这类信息。

没有 `hooks.json` 时，一切和以前完全一样。

## 三分钟上手

1. 打开 **设置 → 协作方式 → 用户钩子**，看"配置文件"一行：这就是 `hooks.json` 该放的位置（数据目录，和 `agentgo.db` 同一个文件夹，通常是 `C:\Users\<用户名>\AppData\Local\hooks.json`），点右边的按钮复制路径。
2. 在这个位置新建 `hooks.json`，写入：

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "findstr /C:\"rm -rf\" >nul && (echo 公司规定：不允许执行 rm -rf 1>&2 & exit /b 2) || exit /b 0", "timeout": 10, "onFailure": "block" }
        ]
      }
    ]
  }
}
```

3. 回到设置页点 **重新读取**（不点也行，下一次触发时会自动读）。状态应显示"已启用 1 个钩子"。之后智能体再想运行包含 `rm -rf` 的命令，在你批准之后、真正执行之前会被拦下，模型会看到"公司规定：不允许执行 rm -rf"。

写错了也不会让钩子悄悄全部消失：整个文件不是合法 JSON 时，继续使用上一份有效配置，设置页用红字写出错在哪；单个钩子写法不对时，只跳过那一个并指出位置，其余照常。

## 四个时机

| 事件 | 什么时候运行 | 脚本能做什么 |
| --- | --- | --- |
| `UserPromptSubmit` | 你发出一条消息之后、模型看到它之前 | 拒绝这条消息（模型不会被调用，你会收到"[钩子拦截] …"）；或输出一段文字，作为补充上下文接在消息后面交给模型 |
| `PreToolUse` | 工具真正运行之前。需要你确认的工具，是你**批准之后**才轮到钩子 | 拦下这一次调用，模型会看到拦截原因 |
| `PostToolUse` | 工具运行完之后 | 只能记录，不能改结果；说"拦截"也只会被记一笔 |
| `Stop` | 智能体答完一轮之后 | 发通知、写审计；在后台运行，不拖慢界面；同时最多 8 个在途，更多的会被跳过并写日志 |

> `execute_bash` 这类高风险工具本来就要你批准，所以你会**先**看到批准弹窗，批准之后钩子才有机会拦。想在弹窗之前就禁止，现在做不到。

## 配置怎么写

```json
{
  "hooks": {
    "事件名": [
      {
        "matcher": "匹配哪些工具（可省略）",
        "hooks": [
          { "type": "command", "command": "要运行的命令", "timeout": 30, "onFailure": "continue" }
        ]
      }
    ]
  }
}
```

| 字段 | 说明 |
| --- | --- |
| `type` | 目前只有 `command`；写别的会被跳过并在设置页提示 |
| `command` | 一整行命令，交给系统的命令解释器运行（Windows 上是 `cmd`，路径含空格要自己加引号）。工作目录是当前工作区根 |
| `timeout` | 秒，可以带小数。默认 30，最大 600。超时会结束命令启动的整棵进程树 |
| `onFailure` | 脚本自己出了问题（起不来、崩溃、超时、退出码不是 0 和 2）时怎么办：`continue`（默认）放行并留一条警告；`block` 拦截。安全类钩子建议写 `block` |
| `matcher` | 只对 `PreToolUse`、`PostToolUse` 有意义。省略、空或 `*` 表示所有工具；只含字母数字下划线和 `\|` 时按名字精确匹配，如 `Bash\|Edit`；其余按正则搜索。`UserPromptSubmit`、`Stop` 忽略它 |
| `async` | 暂不支持，写了会提示，钩子仍会运行并等它结束 |

每个事件最多 20 个钩子，按文件里的顺序运行，第一个拦截的生效，后面的不再运行。同一个钩子同一时刻只运行一个实例（并行的几次工具调用会排队）。

**工具名**：`matcher` 既认 AgentGo 自己的工具名（如 `execute_bash`、`code_edit`），也认 Claude 的写法：

| 可以写 | 对应 |
| --- | --- |
| `Bash` | `execute_bash` |
| `Read` / `Write` / `Edit` / `MultiEdit` | 文件查看、写入、编辑、批量编辑工具 |
| `Glob` / `Grep` / `LS` | 文件查找、内容搜索、列目录工具 |
| `WebSearch` | `web_search` |

脚本收到的 `tool_name` 永远是 AgentGo 自己的名字（比如 `execute_bash`）。

## 脚本收到什么、怎么回话

**输入**：一份 JSON 写在脚本的标准输入里（UTF-8）。共有字段：

| 字段 | 内容 |
| --- | --- |
| `session_id` | 当前对话的编号 |
| `cwd` | 工作区根目录 |
| `hook_event_name` | 事件名 |
| `transcript_path` | 恒为 `null`（AgentGo 没有这个文件） |

各事件另有：

| 事件 | 额外字段 |
| --- | --- |
| `PreToolUse` | `tool_name`、`tool_input`（对象）、`tool_use_id` |
| `PostToolUse` | 同上，再加 `tool_response`（工具输出，最多 64 KiB）；工具出错时有 `tool_error` |
| `UserPromptSubmit` | `prompt`（你发的原文） |
| `Stop` | `last_assistant_message`（最后一条回复，最多 64 KiB）、`stop_hook_active`（恒为 `false`） |

另外有环境变量：`AGENTGO_PROJECT_DIR`、`CLAUDE_PROJECT_DIR`（都是工作区根）、`AGENTGO_HOOK_EVENT`、`AGENTGO_SESSION_ID`。

**输出**：靠退出码和输出文字。

| 退出码 | 意思 |
| --- | --- |
| `0` | 没意见，往下走。可以在标准输出打印 JSON 表达别的意思，见下 |
| `2` | **拦截**。标准错误里的文字就是原因（没写原因会有默认说明） |
| 其他 | 脚本自己出了问题，按 `onFailure` 处理 |

退出码 0 时标准输出的 JSON（字段名与 Claude Code、Codex 一致，已有的脚本可直接用）：

| 写法 | 效果 |
| --- | --- |
| `{"decision":"block","reason":"原因"}` | 拦截（`PreToolUse`、`UserPromptSubmit`） |
| `{"continue":false,"stopReason":"原因"}` | 同上 |
| `{"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":"原因"}}` | 拦截（`PreToolUse`） |
| `{"hookSpecificOutput":{"additionalContext":"补充内容"}}` | 给模型补充上下文（`UserPromptSubmit`） |
| `{"systemMessage":"提示"}` | 只记录 |
| `permissionDecision` 的 `allow`、`ask`，以及 `updatedInput` | **暂不支持**：会在最近运行里报告并忽略，不会做一半 |

`UserPromptSubmit` 的标准输出如果不是 JSON，整段文字就当作补充上下文。

**编码**：写给脚本的 JSON 是 UTF-8。Windows 上的 Python 默认按 GBK 读标准输入，中文会乱，请这样读：

```python
import json, sys
payload = json.loads(sys.stdin.buffer.read().decode("utf-8"))
```

脚本输出 UTF-8 或中文 Windows 默认的 GBK 都能正常显示，不需要特意转换。

## 几个现成例子

**拦截危险命令（Python）**

```python
import json, sys

payload = json.loads(sys.stdin.buffer.read().decode("utf-8"))
command = payload.get("tool_input", {}).get("command", "")
if "rm -rf" in command or "del /s" in command.lower():
    sys.stderr.write("公司规定：不允许批量删除，请换成逐个文件处理")
    sys.exit(2)
```

```json
{ "type": "command", "command": "python D:\\tools\\guard.py", "timeout": 10, "onFailure": "block" }
```

**每次答完一轮，把最后一条回复记进文件（Stop）**

```json
{ "type": "command", "command": "more >> \"D:\\logs\\agentgo-replies.jsonl\"" }
```

`more` 会把标准输入的内容追加到文件，一行一条 JSON，之后可以用任何工具读。

**给每条消息补一句上下文（UserPromptSubmit）**

```python
import json, sys, datetime
sys.stdin.buffer.read()
sys.stdout.buffer.write(("今天是 %s。" % datetime.date.today()).encode("utf-8"))
```

**消息里出现某个词就拒绝（UserPromptSubmit）**：读 `payload["prompt"]`，命中就 `sys.stderr.write("原因")` 然后 `sys.exit(2)`。你会收到一条"[钩子拦截] 这条消息没有发给模型。原因：…"，模型不会被调用，这条消息也不会出现在之后的模型上下文里。

## 在设置页看效果

**设置 → 协作方式 → 用户钩子**：

- 顶部一句话：已启用几个钩子 / 有几处写法问题 / 配置文件有错（仍用上一份）/ 还没有配置 / 已关闭。
- 配置文件路径、最近读取时间、内容校验（前 8 位，用来核对"读到的是不是我刚改的那份"）。
- 四个时机各自配了哪些钩子：命令、匹配范围、超时、出错时放行还是拦截。
- **最近运行**：时间、时机、工具、结果（放行 / 已拦截 / 脚本出错 / 超时 / 已取消）、耗时、原因。只保留本次启动后最近 200 条，在内存里，重启就清空。
- **重新读取**：立刻重读 `hooks.json`，不等下一次触发。

## 安全说明

- 钩子是**护栏，不是沙箱**：脚本以你的身份运行，能做你能做的任何事；而已经获准运行的命令同样能写任何地方，包括 `hooks.json` 所在的文件夹。它防的是"无意间做了不该做的事"，不防有意绕过。
- 只读取数据目录里的这**一个**文件。工作区里的 `hooks.json`、`.agentgo/hooks.json` 都不会被读取，所以克隆或打开一个陌生项目，不会因此在你的电脑上运行命令。
- 智能体自己的文件工具写不了数据目录（路径授权直接拒绝），所以模型不能靠改写文件来关掉你的钩子；文件内容的校验值显示在设置页。
- 钩子脚本里如果又启动了 AgentGo，会被识别（环境变量 `AGENTGO_IN_HOOK=1`）并自动停用，防止递归。
- 每个钩子每路输出最多保留 64 KiB；运行时受和 `execute_bash` 相同的内存、进程数限额。

## 常见问题

**钩子没生效。** 先看设置页：状态是不是"已启用"；"最近运行"里有没有这一条。没有记录，多半是事件名大小写不对（要写 `PreToolUse` 这样的原名）、`matcher` 没选中这个工具（工具名是 `execute_bash` 而不是 `bash`；写 `Bash` 可以），或者这一步根本没走到钩子（还在等你批准、被你拒绝、被策略否决的调用不会触发）。

**为什么先弹批准窗口，钩子之后才拦。** 钩子运行在批准之后、执行之前，见"四个时机"。

**提示"脚本出错"，原因是 `python: command not found`。** 办公电脑没装 Python，或不在 PATH。给命令写完整路径，或换成不依赖 Python 的写法（如上面的 `findstr` 例子）。`onFailure` 为默认的 `continue` 时，这种情况只留警告、不拦截；写了 `block` 则会拦住所有被选中的调用。

**中文原因显示成乱码。** 引擎会把非 UTF-8 的输出按系统 ANSI 代码页（中文 Windows 上是 GBK）读成中文，一般不用处理。读标准输入时的乱码见上面的"编码"。

**怎么整体关掉。** 设置环境变量 `AGENTGO_USER_HOOKS=0` 后重启；或在运行中停用 `userhooks` 插件（插件管理的停用/重新加载，重新加载即恢复，"最近运行"会清空）。关闭时设置页会写明"钩子功能已关闭"。

**能在项目里放一份给团队用吗。** 现在不能：只读数据目录里的文件。以后若支持项目级钩子，会要求先确认文件指纹。
