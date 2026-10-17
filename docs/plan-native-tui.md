# 原生全屏 TUI 设计（plan-crush-digestion D5/D6）

状态：2026-10-17 立项。用户决策：**解冻 tui/cli；终端形态 = 新写 BubbleTea 全屏 TUI；附着桌面宿主 = 丢弃**。

## 1. D5 裁剪决策（只决策）

Crush 里这些子系统只被 Crush TUI 使用，AgentGo 内核不依赖，**全部随 `third_party/crush` 丢弃**，不迁移：

| 子系统 | 决策 | 理由 |
|---|---|---|
| `oauth/*`（Copilot/Grok/OpenAI 登录） | 丢弃 | AgentGo 用自己的 LLM 设置；令牌存储+回调服务器是长期负担 |
| `hooks` | 丢弃 | AgentGo 有 governance 中间件与 plugin 事件 |
| `commands`（自定义 slash 命令） | 丢弃 | 终端内 `/` 命令用原生固定集合；技能走 `skills` |
| `update`、`herdr` | 丢弃 | 发布走自己的构建；无产品理由 |
| `crushproto` 服务端 + `hostlink` 插件 + `crushproto-probe` | 丢弃（用户决策） | 唯一客户端是 Crush TUI；附着功能丢弃，会话仍共享 SQLite（`-continue` 可接着聊，`approvals` 可处理桌面留下的待审批） |

## 2. 系统结构

```
cmd/agentgo-cli ── main: open standalone backend ──┬─ 非 TTY / run / sessions / ...  → internal/cli（行模式，不变）
                                                    └─ TTY 的交互聊天（默认）          → internal/tui（BubbleTea）
internal/tui ── imports internal/cli（只用 Backend/Message/Result/SessionInfo 契约）+ engine（事件）
internal/cli ── 通过 IO.TUI 钩子拿到 tui 入口（cli 不 import tui，避免环）
```

- 一个二进制：`agentgo-cli`。交互聊天在 TTY 下默认进 TUI，`-plain` 强制行模式；`agentgo-cli tui` 显式进入。`cmd/agentgo-tui`、`crushproto-probe` 删除。
- 删旧 `internal/tui`（buffer/renderer/view 约 400 行死代码，无引用）。
- 依赖：`charm.land/bubbletea/v2`、`bubbles/v2`（textarea、viewport）、`lipgloss/v2`、`glamour/v2` 已在 go.mod；D7 后它们是直接依赖，Crush 其余依赖由 `go mod tidy` 清走。

## 3. 数据结构

```go
type Model struct {
    be   cli.Backend; opts Options
    session string
    entries []entry            // 对话时间线（只追加；末尾可原地增长）
    mode    mode               // idle | running | approving | asking | picking | help
    pending []cli.Message      // 待决策队列（approval/question）
    yolo, thinking bool
    // widgets
    vp viewport.Model; in textarea.Model; spin spinner.Model
    width, height int
}
type entry struct {
    kind  kind    // user | assistant | reasoning | tool | diff | notice | err
    text  string  // assistant 流式累加；tool=调用摘要
    tool  *toolEntry // name,args,result,isErr,done
    path, diff string // diff 条目
    md    string  // assistant 完成后的 glamour 渲染缓存（按宽度失效）
}
```

消息（`tea.Msg`）：`evMsg{engine.Event}`（经节流批处理）、`runDoneMsg{res engine.RunResult}`、`resolvedMsg{cli.Result}`、`sessionsMsg`、`tickMsg`（16ms 刷新 token 缓冲）、`tea.WindowSizeMsg`、`tea.KeyPressMsg`。

## 4. 数据流

1. 提交输入 → 追加 user 条目 → `mode=running` → `tea.Cmd` 在 goroutine 里 `be.Run(session, input, sink)`；`sink.Emit` 只把事件写入带锁缓冲，不直接 `Send`（避免 token 洪水重绘），`tickMsg` 每 16ms 排空缓冲并 `Update`。
2. 事件映射：`token`→追加到当前 assistant 条目；`reasoning`→reasoning 条目（`/thinking` 才显示）；`tool_call`/`tool_result`→合并成一个 tool 条目；`file_change`→diff 条目（用事件自带的 `unified_diff`，红绿着色）；`error`→err 条目。
3. `runDoneMsg`：有 `Pending` 消息 → 入队 → `mode=approving/asking`；否则 `idle`。assistant 条目用 glamour 渲染（按当前宽度缓存）。
4. 审批浮层：显示工具名与参数；`edit/multiedit/write` 额外显示将发生的变更预览（从参数构造 diff：edit 的 old/new，write 的新内容头部）；键位 `y` 同意 / `n` 拒绝 / `a` 之后全部同意（yolo）。决策 → `be.Approve` 的 `tea.Cmd` → `resolvedMsg` → 新消息可能又含 pending，循环到队列空。`question`：输入框作答 → `be.Answer`。
5. 取消：运行中 `Esc`/`Ctrl+C` → `be.Cancel(session)`；空闲时 `Ctrl+C` 两次或 `Ctrl+D` 退出。
6. `/` 命令（与行模式一致）：`/new /sessions /resume /yolo /thinking /plugins /tools /help /exit`；`/sessions` 与 `/resume` 打开会话选择浮层（上下选择、回车切换，切换后从 DB 载入历史条目）。
7. 窗口缩放：重排 viewport/textarea，清空 `md` 缓存。

失败路径：后端错误 → err 条目 + 回到 idle（会话不丢）；渲染 glamour 失败 → 退回纯文本；终端宽度 < 40 或高度 < 8 → 提示"窗口太小"。

## 5. 功能对齐清单（打勾即对齐）

- [x] 流式输出（含 reasoning 开关）
- [x] 审批交互（y/n/a）+ 变更预览 diff
- [x] 提问回答（question）
- [x] 文件变更 diff 展示（`file_change` 事件）
- [x] LSP 诊断提示（工具结果里的 `<file_diagnostics>` 块高亮；状态栏显示 LSP 服务器数/错误数）
- [x] 会话切换（浮层）与历史载入
- [x] `/` 命令
- [x] 状态栏：会话、工作区、运行态/spinner、yolo
- [x] 取消、退出、窗口缩放

## 6. 测试策略

- 模型层（`Update`/`View`）用假 `cli.Backend` 做纯函数式测试：事件流 → 时间线、审批队列状态机、yolo、取消、`/` 命令、会话切换。
- 一条端到端：`tea.NewProgram(model, WithInput(pipe), WithOutput(buf))` 无头跑一轮"提问→工具审批→回答→退出"，断言输出含关键文本。
- 保持 `internal/cli` 行模式测试不变（非 TTY 路径不受影响）。
- 每文件 ≤900 行，按 `model.go / update.go / view.go / entries.go / approve.go / slash.go` 拆分。

## 7. 执行顺序

T1 `internal/tui` 骨架 + 模型 + 测试 → T2 接入 `agentgo-cli`（TTY 默认进 TUI，`-plain`）并删旧 `internal/tui` → T3 删 Crush 侧（`cmd/agentgo-tui`、`crushproto-probe`、`internal/crushproto`、`bridge` 的 crushproto/hostlink 装配、`internal/hostlink`、脚本、冻结测试）→ D7 删 `third_party/crush`、`go mod tidy`、全新 clone 构建验证。每步测试全绿再进下一步。

## 8. 结果（2026-10-17）

- T1/T2：`internal/tui`（entries/update/view/approve/slash/sink/style；tui_test + e2e_test 真跑 tea.Program）；`agentgo-cli` 在 TTY 下默认进 TUI，`-plain`/`-json`/管道/`run` 走行模式，`agentgo-cli tui` 显式进入（非 TTY 报用法错误）。`bridgeBackend.History` 供 /resume 重放历史。
- T3：删除 `cmd/agentgo-tui`、`cmd/crushproto-probe`、`cmd/agentgo-headless`（只服务 Crush 协议）、`internal/crushproto`、`internal/hostlink`、bridge 的 crushproto/hostlink 插件与测试、相关 flag（`AGENTGO_CRUSHPROTO_*`、`AGENTGO_HOSTLINK*`）、能力项 `protocol.crush`/`host.attach`；`crushproto_history.go` 里的会话转写服务是通用的，改名 `transcript_svc.go` 保留。
- 冻结白名单清空：`./internal/...` 与 `./cmd/...` 对 `third_party/crush` 的依赖 = 0；`third_party/crush` 仅剩磁盘上的 75 包 / 100,331 行，只被 `-tags crushparity` 的对拍测试引用。
- 与 Crush TUI 的已知差距（有意不做）：无鼠标交互、无文件选择器/命令面板、无图片附件、无多会话并行标签；附着桌面宿主已按决策丢弃。
## 真实终端验证(ConPTY + gpt-6-luna)

用 ConPTY 起真实的 `agentgo-cli.exe`,配一个小型 VT 模拟器读屏,脚本走完:启动 → 提问流式回答 → 请求改文件 → 审批面板(带参数预览)→ `y` → 工具结果 → `/help` → `/sessions` 选单 → Ctrl+D。结果:全流程可用,磁盘上 `a.txt` 确实被改。跑出来并已修的 3 个问题(测试里没覆盖到的):

1. **审批后的续跑看不到工具调用/diff/流式回复。** 续跑段的事件只发给桌面事件总线,CLI 拿不到。新增 `bridge.ResolveApprovalTap` + `tui.Streamer`(`ApproveStream`),续跑事件同时送给终端;`onResolved` 按 `streamed` 去重,避免回复出现两次。
2. **流式回复结束后不渲染 markdown。** `closeStreams` 没递增 `rev`,视图不重绘。已修并加测试。
3. **忙时按 Enter 没反应、输入被吞。** 现在普通消息排队,当前回合结束后自动发送;斜杠命令忙时拒绝并提示;取消/出错会清空队列。

验证用的 ConPTY 驱动是一次性脚本,未入库。
