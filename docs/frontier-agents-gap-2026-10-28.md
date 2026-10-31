# 与 Claude Code / Codex / Cursor 的差距（2026-10-28）

写给要决定下一步做什么的人：结论在前，证据在后。

资料来源与限制：

- 竞品一侧来自 2026 年 10 月的官方更新记录：Claude Code 的 changelog 和 What's new 周报（2.1.29x–2.1.31x）、Codex 0.163–0.165 发布说明（本地还有 `../codex-reference` 的源码克隆）、Cursor 10 月 20 日的 Agent SDK 发布和 10 月 12 日当周 Cloud Agents 更新。
- AgentGo 一侧来自读代码，文中给出文件路径。
- 没有逐项实机试用竞品。表里标 † 的竞品能力，是我凭既有了解写的，本轮没有在发布说明里再核对。
- **2026-10-30 更正**：对照表有 7 处与代码不符（并行工具调用、改动撤销、前端"回滚检查点"按钮、中途纠偏、可观测、模型与工具协同、"不追"清单），已在对应行里以"更正（2026-10-30）"标出，原文保留。核实过程见 `docs/architecture-clarity-and-harness-gap-2026-10-30.md` 第 1 节。

## 结论

1. 差距不在"能不能跑起一个 agent 循环"。循环、上下文压缩、记忆、审批、评测、注入测试 AgentGo 都已成体系，记忆和治理比它们做得更深。
2. 真正的差距有六块，按对实际工作的影响排序：
   1. 改动没有统一的撤销和检查点（更正 2026-10-30：整会话级的接受/拒绝已有，缺口是按轮次回退、入口无关、重启保留，见对照表）。
   2. 命令没有系统级隔离。
   3. 缺少质量证据：评测集太小，没有换模型后的对比成绩。
   4. 缺少给用户用的钩子和自定义命令。
   5. 并行与后台：会话分叉、后台继续、并行子智能体的可视化。
   6. 代码审查和 Git/PR 这条链路（只对开发场景）。
3. 有一个差距无法靠写代码消除：它们的模型是和自己的工具一起训练的，AgentGo 接的是通用网关模型。只能靠评测去测、靠工具格式去适配、靠选模型去缩小。
4. 不用追的：云端 agent、手机遥控、浏览器/模拟器/电脑操作、语音、插件市场。办公内网用不上，或者做不到。（更正 2026-10-30：电脑操作、浏览器控制、IM 渠道后来都做了，见 `docs/octo-gap-closure-2026-10-29.md`；仍不做的是云端 agent 和模拟器，手机端暂停。）

## 对照表

差距一栏的取值：领先、持平、小差距、大差距、待核实、不追。

| 维度 | 顶尖产品 | AgentGo 现状（证据） | 差距 |
| --- | --- | --- | --- |
| 长任务上下文 | 三家都有自动压缩；Codex 有压缩检查点；Claude Code 的新默认模型有 1M 上下文 | 自动压缩 + 摘要树 + 按模型窗口算预算（`internal/agent/context_compaction.go`、`internal/promptbudget`）；前缀稳定，缓存命中 85%–88%（`docs/eval-baseline.md`） | 持平 |
| 记忆 | Codex 的 memory v2 对新用户默认开启，还能导入别家 agent 的记忆；Claude Code 靠 CLAUDE.md 和自动记忆；Cursor 有 Memories 和 rules | 层次摘要树 + 证据溯源 + 可视化 + 个人学习（`internal/memory`、`browse_memory`） | 领先 |
| 子智能体 | Claude Code 的 fork 模式（子智能体继承完整对话）和 `claude agents` 视图；Cursor 子智能体并行；Codex 有 agent 指挥中心 | `invoke_subagent`、`run_swarm`（`internal/agent/extra_tools.go`），嵌套深度上限 4 层（`internal/governance/subagent_monotone.go`）；没有逐个查看子智能体对话的并行视图 | 小差距 |
| 中途纠偏 | Codex 0.163：智能体还在回复时直接发消息纠偏；Cursor 云端 agent 收尾时收到的消息并入同一轮 | 已有：`SteerSession` 接口加每个会话的纠偏收件箱（`internal/bridge/app_ipc.go`、`internal/agent/run_control.go`、`runner.go`），前端已接入（`frontend/src/stores/useChat.ts`）。**更正（2026-10-30）**：纠偏消息排进队列，要等当前这一轮（含所有工具调用）结束后才由 `maybeContinue` 取走（`DrainSteers` 全仓库唯一的调用点，`internal/agent/runner.go`），再作为新的一轮发出，模型在长工具链中途看不到；`AddSteer` 的注释写"mid-turn"，与实现不符 | 小差距 |
| 编辑原语 | Codex 的 `apply_patch`、Claude Code 的 Edit/MultiEdit †、Cursor 的专用应用模型 † | `codetools` 插件提供 `edit`、`multiedit`、`view`、`write`、`grep`、`glob`、`ls`，加 LSP 诊断/引用工具（`internal/bridge/codetools_plugin.go`、`internal/coderuntime`、`internal/codelsp`）；写入后有回调把语言服务诊断附到工具返回里（`FSOptions.AfterChange`）；改文件的工具在非"开放"模式都要审批。没有 `apply_patch` 式统一 diff，国产模型本来也不是按它训练的 | 持平 |
| 并行工具调用 | Codex 0.163：只并行独立的只读工具（技能、记忆、历史查找） | 同一轮的多个工具调用走 Eino 默认的并行执行，写文件的也一样（`ExecuteSequentially` 没有设置）。文件工具共享一把状态锁（`internal/coderuntime/fs_tools.go`），同一文件的并发写入顺序是否有保证，尚未核实。**更正（2026-10-30）**：已解决。`internal/agent/tool_barrier.go` 让同一轮回复里改文件的、没声明特性的调用按顺序执行，只并行声明了只读且低风险的调用（起因是框架会先跑最后一个调用）；默认开，`AGENTGO_TOOL_BARRIER=0` 关闭 | 持平 |
| 改动撤销与检查点 | Claude Code 的 `/rewind` †、Cursor 的检查点 †；Codex 有托管 Git worktree | 没有通用的文件快照。只有 Excel 改前自动备份、扩展版本快照（`internal/agentpack/versions.go`）。**更正（2026-10-30）**：有整会话级的撤销。`internal/workspace/review.go` 在每次发送前对整个工作区建一份内存基线（单文件不超过 8 MiB、总量不超过 128 MiB，超限的只记哈希、不可还原），可逐文件或全部接受/拒绝；因为是对文件系统做比对，shell 命令造成的改动也能还原。缺口：只有前端的发送入口会建基线，IM、网关、ACP、CLI 没有；基线在内存里，重启即丢；一个会话一份基线，不能回到第 N 轮前；不联动对话上下文。前端"回滚检查点"按钮原来写"回滚到此轮提问前"，实际是拒绝本会话全部改动，2026-10-30 已把文案改成与实现一致（"撤销未接受改动"） | 中差距 |
| 代码审查 | Claude Code 的 `/code-review`（后台子智能体）和 Security 插件；Cursor 的 Bugbot 与 Security Reviewer；Codex 的 `/review` † | 已补（2026-10-28）：`/review [审查重点]`，网页、桌面、终端界面通用；读取工作区未提交的改动，只报本次改动引入的问题，按 P0–P3 分级（`internal/core/commands.go`）；`get_workspace_changes` 增加改动统计，并修了 Windows 下 git 警告混进 diff 的问题。不是独立子智能体，在当前会话里运行；没有 PR / Git 平台集成 | 小差距（开发场景） |
| 命令隔离 | Codex：Linux 用 bubblewrap + seccomp，网络代理在 DNS 查询前先检查权限；Claude Code：auto mode 自动判断哪些操作要确认；Cursor 的终端沙箱 † | 审批 + 路径授权（`internal/pathgrant`）+ 环境白名单；2026-10-28 起 `execute_bash` 有默认 180 秒超时（模型最多可要 30 分钟），Job Object 限制一条命令共用的内存（默认物理内存的 70%）和同时存活的进程数（256），崩溃不弹窗（这三项只在 Windows 生效）；取消或超时会结束整棵进程树（`internal/shellcmd`）。仍不限制文件和网络访问 | 大差距（资源限制已补，文件和网络隔离没有） |
| 自动审批 | Claude Code 的 auto mode 已是新会话默认，自动判断哪些操作要确认，并拦截篡改对话记录、`rm -rf` 带未解析变量等 | 规则式审批 + 设置页的"操作确认方式"三档；没有这种自动判断 | 小差距 |
| 提示注入 | auto mode 里有拦截，其余没有公开数据 | 9 例注入评测套件（7 个攻击），外部内容打标（`internal/externalcontent`） | 持平 |
| 钩子 | Claude Code 的命令/HTTP 钩子（`onFailure: "block"` 失败即拦截）和 mod 钩子；Cursor hooks | 底座已有：插件（Go 代码）可以注册工具前后钩子，`Before` 能否决调用，panic 也按否决处理（`internal/governance/tool_hooks.go`）；但它在审批通过之后才运行、不能改参数，没有"提交消息前"和"一轮结束后"两个点位，也没有面向用户的配置入口（命令或 HTTP 钩子）。设计稿：`docs/SYSTEM_DESIGN_2026-10-28.md`。**已补（2026-10-28）**：`hooks.json` 命令钩子，四个时机，设置页可看状态，见 `docs/user-hooks.md`；HTTP 钩子、`updatedInput` / `allow` / `ask`、项目级钩子没做 | 命令钩子已补，其余仍有差距 |
| 技能 | 三家都支持 SKILL.md；Codex 的技能查找可与记忆、历史查找并行执行 | `activate_skill` + `SKILL.md` 加载（`internal/skills`），也读 `.cursor/skills`；正文超过 4000 字节截断，不解析 frontmatter | 小差距 |
| 指令文件 | Codex、Claude Code 层级加载（从仓库根到当前目录）、用户级文件、AGENTS.md 与 CLAUDE.md | 已补（2026-10-28）：数据目录里的用户级 AGENTS.md；从 git 根到工作区根逐层读 AGENTS.md，没有则读 agents.md、CLAUDE.md；SOUL.md / TEAM.md 仍读工作区根；`.cursor/rules` 里 alwaysApply 的整篇注入，只有描述或 globs 的做成一行一条的索引，让模型按需去读（`internal/agent/instruction_files.go`、`rule_frontmatter.go`）。符号链接指到项目外的文件不读 | 持平 |
| 自定义命令 | Codex 的 `/presets`（十个槽位）、Claude Code 的自定义命令与插件、Cursor commands | TUI 有内置斜杠命令（`internal/tui/slash.go`），用户不能自己加 | 小差距 |
| MCP | 三家都有；Codex 的企业 MCP 登录（OAuth）10 月正式可用 | 客户端管理（`internal/tools/mcp_manager.go`）；不能当 MCP 服务端；没有 OAuth 登录；没有管理界面 | 小差距 |
| 编程接入 | Claude Agent SDK、`codex exec` + SDK、Cursor Agent SDK（10 月 20 日正式发布，含事件触发、评测、事件回放） | `agentgo --serve` HTTP 网关、`agentgo-cli run -json`、ACP 服务端（`internal/acp`）；没有发布 SDK | 小差距 |
| 事件触发自动化 | Cursor：PR、CI、Slack、Webhook、定时触发 | `internal/trigger`（文件/进程监听）、`internal/scheduler`（cron）、`watch_event`、IM 渠道 | 持平 |
| 会话分叉与后台并行 | Claude Code 的 `/fork` 后台会话、`--worktree`、跨会话互发消息；Codex 的会话分叉（实验）、托管 worktree、按仓库过滤的会话选择器 | 多会话并行已实测可用（3 个会话同时跑，事件按会话分流，见 `docs/xlsx-access-elevation-2026-10-27.md`），侧栏有运行/等待徽标，子智能体以工具卡片出现在转录里，后台任务有任务面板（`internal/agent/bgtask.go`）。没有分叉：`internal/sessions/tree.go` 的 `ForkLane` 操作的是一份没有生产调用方的独立数据（2026-10-30 已删除该文件，两张表只作遗留空表保留），模型上下文的真源是 `chat_context_events`，所以要做复制式分叉；也没有集中的"运行中"视图。设计稿：`docs/SYSTEM_DESIGN_2026-10-28.md`。**已补（2026-10-28）**：复制式分叉（侧栏右键、`/fork`、谱系标签），见设计稿 8.2；集中的"运行中"视图（状态栏按钮 + 弹层，列出在跑的对话、等你批准或回答的、后台任务，可跳转、停止、取消，刷新页面后仍然正确），见设计稿 8.3（原来的任务页读错了字段、后台任务列表一直是空的，一并修了）；独立 git 工作树、从第 N 轮前分叉、子智能体行还没有 | 分叉和运行中视图已补，其余仍有差距 |
| Git / PR | 三家都与 GitHub/GitLab 深度整合 | 只有界面侧的 Git 面板（`internal/ideruntime/git_service.go`）；智能体没有 git/PR 工具 | 大差距（开发场景） |
| 质量证据 | Cursor 的 Agent SDK 自带 evals 和事件回放 | `agentgo-eval`：默认套件 20 例、工作区套件 3 例、注入套件 9 例；代码闭环有 `scripts/ab_codetools.py`（27 次运行）；没有换模型的横向成绩 | 大差距 |
| 可观测 | Claude Code 输出 OpenTelemetry（GenAI 语义，缓存写入分 5 分钟/1 小时）；Codex 也有 OpenTelemetry 导出 † | 用量账本、诊断包、日志；不导出 OTel。**更正（2026-10-30）**：曾有一个写好但没接线的 Langfuse/OTel 导出器（`internal/telemetry`，`InitializeOTel` 没有调用方，整包无人导入），当日已删除，需要时可从 git 取回 | 小差距 |
| 模型与工具协同 | Claude、GPT、Composer 与各自的工具一起训练 | 通用网关模型（Qwen、DeepSeek、GLM 等），工具格式没有按模型调过。**更正（2026-10-30）**：调过，但没收口。延迟工具的提醒位置按 DeepSeek 实测改过（同一请求 0/8 → 7/8 可用回复，`internal/agent/toolsearch_reminder.go`）；`reasoning_effort` 等在构建处按模型名硬编码（`internal/agent/adk_agent_build.go`）；缺的是集中的"模型档案"和换模型的回归评测 | 大差距（结构性） |
| 终端体验 | 键位自定义、设置搜索、图片预览、屏幕阅读器模式 | Bubble Tea TUI + CLI REPL，功能较少；但有完整的桌面和浏览器界面 | 小差距 |
| 编辑器整合 | VS Code、JetBrains 扩展 | ACP（可接 Zed 等）；自带工作台（LSP、终端、搜索、Git） | 小差距 |
| 云端、远控、手机 | Claude Code 远控与自托管云会话；Codex 云；Cursor Cloud Agents | 刻意不做。**更正（2026-10-30）**：`agentgo-web --remote` 与 IM 双向渠道已存在，手机端暂停，云端 agent 不做 | 不追（云端） |
| 浏览器、模拟器、电脑操作 | Claude Code 内置浏览器和 iOS 模拟器；Cursor 浏览器和电脑操作 | 刻意不做。**更正（2026-10-30）**：口径已变，电脑操作（`internal/computeruse`、`cmd/agentgo-cua`，含 UIA 语义点击）和浏览器控制（`internal/browsercontrol`）已经做了，见 `docs/octo-gap-closure-2026-10-29.md`；模拟器仍不做 | 不追（模拟器） |
| 办公文档 | Claude 有 docx / xlsx / pptx / pdf 技能 †，但要联网；Codex、Cursor 基本没有 | xlsx / docx / pptx 在本机直接读写、工作簿提权访问、改前备份 | 领先（内网本地场景） |
| 内网、Win7、单 exe | Claude Code、Codex 能接内部网关和云厂商模型，但都不支持 Win7，面向开发者终端；Cursor 依赖自家服务 | 全部编译进一个 exe，Win7 能跑，有图形界面 | 领先（这个使用场景） |
| 内置应用与工作流 | Cursor 的"agent 即代码"最接近 | Inner App + 工作流 + AgentPack（版本、回滚、导入导出） | 持平 |
| 治理与审计 | 企业版有，Claude Code 的 auto mode 在加强 | 审批、审计、工具钩子、子智能体深度上限、提权阶段（`internal/governance`） | 持平 |

## 要保住的优势

- 内网单 exe 和 Win7：Claude Code、Codex 能接内部网关，但都不支持 Win7，也面向开发者终端；Cursor 依赖自家服务。办公室里的 Win7 和非开发者用户，这三家都不覆盖。
- 办公文档：Claude 的文档技能要联网，Codex 和 Cursor 基本没有；AgentGo 在本机直接读写，还带改前备份。
- 记忆树的证据溯源和可视化：目前公开资料里没看到同样可浏览、可追溯的层次结构。
- 治理：审批、审计、插件级工具钩子、子智能体深度上限、注入评测。办公环境更看重这些，而且已经成体系。
- 评测和前缀稳定性：缓存命中 85%–88% 来自专门的工程，后面改提示词和工具集不要破坏它。

## 建议顺序

工作量只是估计：S 小于一天，M 一到三天，L 一周以上。

| 优先级 | 事项 | 工作量 | 为什么 |
| --- | --- | --- | --- |
| P0（已完成） | 指令文件兼容：子目录层级、用户级文件、CLAUDE.md 回退、`.cursor/rules` 按 alwaysApply/globs 注入 | S | 现在全部注入会浪费上下文，也让别家项目的规则用不上 |
| P0（已完成） | 一键代码审查：对工作区变更跑审查子智能体，按严重程度分级输出 | S | 已有 `get_workspace_changes` 和子智能体，拼装即可 |
| P0（已完成） | `execute_bash` 的 Job Object 资源上限（内存、进程数、超时） | S | 已有 Job Object 基础，不引入新依赖 |
| P1（部分已有） | 文件检查点与一键撤销：写入前快照，会话内回退到某条消息 | M | 整会话级的接受/拒绝已有（见对照表更正）；缺按轮次回退、所有入口都建基线、重启后保留 |
| P1（已完成） | 用户钩子：工具前后、提交消息前、一轮结束后；失败策略按条配置 | M | 企业合规需要（禁止写某目录、写审计、提交前检查）；底座 `RegisterToolHook` 只覆盖审批之后的工具前后，其余点位和配置入口要新做；设计稿见 `docs/SYSTEM_DESIGN_2026-10-28.md` |
| P1 | 评测扩充到 100 例以上，建立模型矩阵跑分，并统计编辑工具的失败重试率 | L（持续） | 决定"好不好用"，也是选模型和调工具格式的依据 |
| P1（已完成） | 核实并发写入：同一轮里的多个写文件调用，要么串行，要么按文件加锁 | S | 2026-10-30 核实：`internal/agent/tool_barrier.go` 已让改文件的调用按顺序执行 |
| P2（已完成） | 会话分叉、运行中视图（后台继续本来就可用） | M | 并行运行和后台任务已有；已补复制式分叉、入口和集中视图，设计稿和施工记录见 `docs/SYSTEM_DESIGN_2026-10-28.md`；子智能体行、独立 git 工作树、从第 N 轮前分叉没做 |
| P2 | 命令沙箱可选档位：受限令牌 + 低完整性级别 | L | Windows 上做得到文件限制，网络隔离做不到，要如实说明 |
| P2 | 自定义斜杠命令、MCP 管理界面与 OAuth 登录 | M | 体验补齐 |
| P3 | OpenTelemetry 导出、发布 SDK、VS Code 扩展、git/PR 工具 | L | 内网用得少，用到再做 |

## 进展（2026-10-28）

三个 P0 已做完，做法、测试和限制见 `docs/p0-quick-wins-2026-10-28.md`。读代码时发现分叉的底座接不上，已在对照表里更正；钩子和分叉的设计稿是 `docs/SYSTEM_DESIGN_2026-10-28.md`，确认后已施工：用户钩子（M1、M2）、会话分叉（M3）和运行中视图（M4）都已完成并验证，施工记录和限制见该文档第 8 节。

## 核实记录

草稿里原来有三项待核实，现在的结果：

- 中途纠偏：已有，见对照表。这一项从"待核实"改成了"持平"。
- 改完文件后回灌语言服务诊断：写入类工具有写后回调，诊断附在工具返回里。
- 多个工具调用是否并行：是，Eino 默认并行，AgentGo 没有改。同一文件的并发写入顺序没有核实，列为 P1 里的一项小事。

子智能体和钩子这两行也按代码更正过：`subagent_monotone.go` 只限制嵌套深度（最多 4 层），草稿原来写成"权限不能超过父级"，没有依据，已删；钩子的底座其实已经有，只缺用户入口。

办公文档和内网两行原来写得太绝对（"基本没有""竞品都要联网"）。Claude 有文档技能，Claude Code 和 Codex 也能接内部网关，已改成更窄的说法。

另外，草稿最初把编辑原语判成"小差距"，是因为只看了 `internal/tools` 下的工具名。`edit`、`multiedit` 等在 `internal/coderuntime` 里，核实后改为"持平"，同时删掉了"补 `apply_patch`"这一项。
