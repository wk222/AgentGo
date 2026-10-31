# AgentGo 与顶级 harness 的差距，以及架构是否清晰（2026-10-30）

写给要决定下一步做什么的人：结论在前，证据在后。

资料来源与限制：

- AgentGo 一侧来自读代码，文中给出文件路径。依赖关系用 `go list`（默认构建标签、Windows）取出，再与 `internal/arch/layers.go` 对照，数字是实测。
- 竞品一侧沿用 `docs/frontier-agents-gap-2026-10-28.md`（引用 2026 年 10 月的官方更新记录），本轮没有重新核对。标 † 的是我凭既有了解写的，同样没有核对。
- 没有试用竞品，没有在浏览器里点过前端；除第 3.1 节注明的一次 linux 交叉编译外，没有在 Windows 以外构建或运行过。读代码得出的和实测得出的，正文里分开标。

## 结论

1. **差距**。循环、上下文压缩、记忆、审批、注入防护、办公文档、写入护栏都已成体系；记忆、治理、办公文档、内网单 exe 比顶级商业 harness 更深，或它们不覆盖。剩下的差距集中在四块，按对实际工作的影响排序：
   1. 命令隔离：主要使用环境 Windows 上没有 OS 级沙箱，只有审批、路径授权和资源上限。**（2026-10-30 夜补了一半：加了只防写的 Windows 沙箱，默认关，见 7.5；防读和防网络仍做不到。）**
   2. 质量证据：评测集小，没有换模型的横向成绩，仓库里也搜不到公开基准（SWE-bench、Terminal-Bench 等）的适配或成绩。
   3. 多任务的可控性：子智能体不可恢复、没有运行表；纠偏要等本轮结束才生效；撤销只能整会话拒绝，不能回到某一轮。**（“回到某一轮”已于 2026-10-30 夜做完，见 7.5；其余没变。）**
   4. 模型适配没有收口：没有辅助小模型分工，模型家族名出现在 23 个文件里（其中 `agent` 包 10 个）。这一项 10-28 没列，是这次读代码新发现的。
2. **架构**。不是混乱，但不清晰。依赖方向管住了（68 个顶层包加 12 个子包，共 80 项全部登记，向上依赖 0 条，没有超过 1500 行的文件），职责归属没管住，层模型和事实已经脱节：
   - `bridge` 实际是系统的内核（113 个文件，直接依赖其余 68 个顶层包里的 47 个，`AppService` 有 295 个导出方法），而设计上作为内核的 `core` 只有 `bridge` 一个调用方。
   - `agent` 是上帝包：84 个文件，`Runner` 25 个字段，中间件顺序散在三处，手写的清单和真实管线对不上。
   - 同一概念散在多个包（审批 4 处、后台任务 5 处、会话 5 种结构）。
   - 唯一的架构总览 `docs/ARCHITECTURE.md` 已落后于代码（2026-10-30 已重写）；`internal/telemetry` 和 `sessions/tree.go` 的 lane 接口是死代码（已删除）。`pkg/mcphost` 我原先也算作死代码，这是错的：它是同级仓库 `agentgo-crush` 的入口，见 3.5。
3. **10-28 的差距表有 7 处与代码对不上**，其中 4 处会改变优先级（并发写入、撤销、纠偏、模型协同），见第 1 节。
4. 最该先做的是第 4 节的 P0：都小于一天，没有架构争议。**已于 2026-10-30 做完，范围与做法见文末"执行记录"。**

## 1. 与 10-28 差距表对不上的 7 处

| 项 | 10-28 的说法 | 核实结果 | 证据 |
| --- | --- | --- | --- |
| 并发写入 | 待核实，列为 P1 | **已解决**。同一轮回复里的多个工具调用，只有声明了只读且低风险的并行；改文件的、没声明特性的，要等它前面的调用结束，后面的也等它。起因是框架会先跑最后一个调用，`[写文件, 跑脚本]` 原来每次都先跑脚本。默认开（`internal/config/flags.go:47`），`AGENTGO_TOOL_BARRIER=0` 关闭 | `internal/agent/tool_barrier.go` |
| 改动撤销与检查点 | 大差距：没有通用的文件快照 | **有，但有四个缺口**。每次发送前对整个工作区建一份内存基线（单文件不超过 8 MiB、总量不超过 128 MiB，超限的只记哈希、不可还原），之后可逐文件或全部接受/拒绝；因为是对文件系统做比对，shell 命令造成的改动也能还原。缺口：① 只有前端的发送入口会建基线，IM、网关、ACP、CLI 进来的会话没有；② 基线在内存里，重启就没了；③ 一个会话一份基线，直到接受或拒绝全部才重建，不能"回到第 N 轮前"；④ 不联动对话上下文。**更新（2026-10-30 夜）：③ 已由新增的按轮检查点补上，④ 做了一半（回退后向会话追加一条说明，模型看得到，但不截断对话）；① 对审阅基线仍成立；② 检查点同样只在内存里。见 7.5** | `internal/workspace/review.go`；`frontend/src/app/AppShell.tsx:86`（`BeginWorkspaceReview` 全仓库唯一的调用点）；`internal/bridge/workspace_review.go` |
| 前端"回滚检查点"按钮 | 未提 | **文案和实现不符**。按钮写"回滚到此轮提问前"，实际调用 `RejectAllWorkspaceReview(sessionId)`；`turnIdx` 只用在提示文字里，没有传给后端，点哪一轮效果都是"拒绝本会话全部改动" | `frontend/src/features/chat/FlowChatTranscript.tsx:256` |
| 中途纠偏 | 持平 | **降为小差距**。纠偏消息排进队列，要等当前这一轮（含所有工具调用）结束后才被取走，再作为新的一轮发出；模型在长工具链中途看不到。Codex 的做法是在下一次模型调用前并入同一轮 † | `internal/agent/run_control.go:131`（`AddSteer`，注释写"mid-turn"）；`internal/agent/runner.go:678`（`DrainSteers` 全仓库唯一的调用点，在 `maybeContinue` 里） |
| 可观测 | 不导出 OTel | **结论对，原因更差**：写了 Langfuse 导出器，但 `InitializeOTel` 没有调用方，整个 `telemetry` 包无人导入，是死代码 | `internal/telemetry/langfuse_exporter.go:243` |
| 模型与工具协同 | 大差距（结构性）；工具格式没有按模型调过 | **调过，但没收口**。延迟工具的提醒位置按 DeepSeek 实测改过（同一请求 0/8 → 7/8 可用回复）；`gpt-5`、`luna` 一类模型的 `reasoning_effort` 在构建处硬编码。缺的是集中的"模型档案"和换模型的回归评测 | `internal/agent/toolsearch_reminder.go`；`internal/agent/adk_agent_build.go` |
| "不追"清单 | 浏览器、电脑操作、手机远控刻意不做 | **口径已变**：电脑操作（`internal/computeruse`、`cmd/agentgo-cua`，含 UIA 语义点击）、浏览器控制（`internal/browsercontrol`）、IM 双向渠道、`agentgo-web --remote` 都已存在；手机端暂停 | `docs/octo-gap-closure-2026-10-29.md` |

## 2. 还有什么差距（修订后）

大小取值：大差距、中差距、小差距。工作量：S 小于一天，M 一到三天，L 一周以上。

| # | 差距 | 顶尖产品 | AgentGo 现状（证据） | 大小 | 建议 / 工作量 |
| --- | --- | --- | --- | --- | --- |
| 1 | 命令隔离 | Codex：Linux 用 bubblewrap + seccomp；Claude Code：auto mode；Cursor 终端沙箱 †（出自 10-28） | Linux 有 Landlock + seccomp，macOS 有 Seatbelt，默认关（`AGENTGO_SANDBOX_OS=1`），只作用于 `execute_bash`，**没有在真机运行过**；**Windows 不支持（fail-closed）**，只有审批、路径授权和 Job Object 的内存/进程数上限（`internal/sandbox/os_*.go`、`internal/shellcmd`）。主要使用环境恰好是缺的这块。**更新（2026-10-30 夜）：Windows 已有只防写的沙箱（受限令牌加低完整性级别），默认关，要设 `AGENTGO_SANDBOX_OS_NET=1` 表示接受“读取和网络不受限”；见 7.5** | 大（Windows 防读、防网络仍缺） | Windows 受限令牌 + 低完整性级别（L，**已做**）；Linux 沙箱的真实验证交给 CI 的 ubuntu 任务（S，未做） |
| 2 | 质量证据 | Cursor Agent SDK 自带评测与事件回放 | 评测套件仍是 3 个文件（`internal/evaluation/suite_{default,workspace,injection}.json`，10-28 记的是 20/3/9 例，本轮没有重数）；办公检索基线 18 例，有 3 个模型的成绩（`docs/office-baseline-*.json`）；通用任务没有模型矩阵；在 `docs`、`internal`、`cmd`、`scripts`、README 里搜不到 SWE-bench、Terminal-Bench 等公开基准。`docs/plan-harness-evolution.md` 的自我改进循环只做到第 2 步，而它自己写明"套件太小，切训练/留出会单薄" | 大 | 扩到 100 例以上并跑模型矩阵（L，持续）；之后再推第 3–6 步 |
| 3 | 撤销 | Claude Code `/rewind` †、Cursor 检查点 † | 见第 1 节：有整会话级拒绝，缺按轮次、入口无关、持久化、联动对话。**更新（2026-10-30 夜）：按轮次已做，持久化与入口无关仍缺，见 7.5** | 中 | 先修按钮文案（S，已做）；基线建立下沉到 `sendMessageCore`，所有入口都有（M，未做）；按轮次基线（M–L，已做，内存版） |
| 4 | 子智能体可控性 | Claude Code 的 fork 与 `claude agents` 视图；Cursor 并行子智能体 | 子智能体不可恢复（10-29 记为不做 `subagent_inspect`）；后端没有可查的运行表，运行中视图里没有子智能体行（10-28 设计稿 8.3） | 中 | 子智能体运行表：登记、查看、停止、恢复（M–L） |
| 5 | 辅助小模型 | Claude Code 用小模型做标题、摘要等后台活 † | `LLMSettings` 只有主模型和故障转移（`internal/agent/runner.go`）；摘要压缩直接用主模型（`middlewares.go` 的 `contextSummaryConfig(baseModel, policy)`）；后台任务通过 `SetLLMProvider` 取同一份配置。记忆提取、日志是否同样走主模型，没有逐个核实 | 中 | 增加"辅助模型"可选配置，路由摘要、记忆提取、标题（M）；没有评测数据前只做可选项 |
| 6 | 模型档案 | Codex 的模型元数据 †（上下文、并行调用等集中配置） | 模型家族名出现在 23 个非测试文件里（粗筛，含价格表、默认值、文案）；`agent` 包有 10 个文件出现这些名字，我只逐个读了 `toolsearch_reminder.go` 和 `adk_agent_build.go` | 中 | 一张表收口：是否支持并行工具调用、system 与 tools 并存、`reasoning_effort`、提醒位置、工具数上限；新增模型先过评测矩阵（M） |
| 7 | 纠偏时机 | Codex 0.163：回复中直接纠偏 | 见第 1 节 | 小 | 在下一次模型调用前注入，用现有的 ADK 中间件钩子，做法同 `tool_barrier.go`（S–M） |
| 8 | Git / PR | 三家都深度整合 | 沿用 10-28：只有界面侧 Git 面板，智能体没有 git/PR 工具；本轮没有复核 | 大（开发场景） | 用到再做 |
| 9 | 用户扩展面 | 自定义命令、MCP OAuth、HTTP 钩子 | 沿用 10-28，本轮没有复核 | 小 | 见 10-28 |

## 3. 架构清晰吗

先说结论：**依赖方向是清楚的，职责归属不清楚。**

### 要保住的

- 依赖守卫有效：`internal/arch` 的测试保证每个包都登记、不能向上依赖、`cmd/agentgo` 只经 `bridge`；Crush 已清除并有守卫（`crush_freeze_test.go`）；子进程必须隐藏窗口、`AGENTGO_*` 只能在 `internal/config` 声明，也都有测试。
- 全库非测试 Go 代码 777 个文件、133,775 行，最大的单文件 1,359 行，没有超过 1500 行的。
- 处理框架问题的深度：`tool_barrier.go` 发现并绕开了"先跑最后一个调用"的框架行为；`toolsearch_reminder.go` 按实测数据改提醒位置。这类细节是顶级 harness 才会做的。
- 文档诚实：10-28 的设计稿有"没有验证"栏，这份文档沿用。

### 3.1 `bridge` 成了事实上的内核，`core` 被架空（影响最大）

| 指标 | 数值 | 来源 |
| --- | --- | --- |
| `bridge` 规模 | 113 个文件、10,960 行；直接依赖其余 68 个顶层包里的 47 个；没有任何内部包依赖它，只有 `cmd` 入口 | `go list` |
| `AppService` 导出方法 | 295 个；Wails 绑定与终端客户端共用（`layers.go` 注释：`cli` 通过 `bridge.AppService` 驱动） | `internal/bridge/*.go`（非测试） |
| `core`（`layers.go` 注释：所有入口共享的业务逻辑） | 18 个非测试文件、2,677 行；唯一调用方是 `bridge` | `go list` |
| 业务编排落在 `bridge` | `sendMessageCore`（`message.go`）、运行中视图的合并规则（`running_work.go`）、批准后续跑（`approval_resume.go`）、分叉（`session_fork_ipc.go`）、钩子接线（`userhooks_*`） | 10-28 设计稿第 8 节 |
| 文档与事实不符 | `docs/ARCHITECTURE.md` 写 bridge"只做 IPC 适配、Runtime 组装、接口注入"，层图是 6 层且 bridge 为 L4；`layers.go` 是 7 层，core 为 L4、bridge 为 L5 | 两个文件对照 |
| 能力依赖入口 | 撤销的基线只在前端发送入口建立，IM、网关、ACP、CLI 的会话没有 | 第 1 节 |
| 可移植性被一个文件决定 | `internal` 里唯一直接引用 Wails 的是 `bridge/host_desktop.go`（`//go:build !web`）。`GOOS=linux CGO_ENABLED=0 go list -deps ./cmd/agentgo-cli`：默认标签 733 个依赖包、21 个 Wails；加 `-tags web` 598 个、0 个 Wails，没有构建约束错误。10-29 文档写"agentgo-cli 依赖 Wails，只能在 Windows 构建，所以没做跨平台发布"，这个约束可以消除。**已真实编译**：`GOOS=linux CGO_ENABLED=0 go build -tags web ./cmd/agentgo-cli` 通过，86 秒，产物 71.1 MB，无警告（没有运行，只确认能编译链接） | `go list`、`go build`；`docs/octo-gap-closure-2026-10-29.md` |

推断（没有统计）：规则只限制依赖方向，`bridge` 在最高的消费层，可以依赖一切，所以它是新功能最省事的落点。这解释了它的体量，但我没有数 `bridge` 里业务逻辑占多少行。

### 3.2 `agent` 是上帝包

- 84 个文件、12,090 行，依赖 21 个包。`Runner` 有 25 个字段：模式、审批策略、检查点、运行控制、记忆、后台任务、心跳、用量、密钥池、范式引擎等都挂在它上面（`internal/agent/runner.go`）。
- 中间件顺序由三处代码共同决定，没有单一清单：`handlers.go`（`BuildADKHandlers`）、`middlewares.go`（`BuildTypedMiddlewareStack`，编号 1–8）、`toolExecutionHandlers`（这个函数我没读）。
- `runner.go:737` 的 `MiddlewareNames()`（2026-10-30 已删除）曾是手写字符串清单，注释写"documents the chain"，已对不上真实管线：`evidence`、`document_view`、`spill`、`armor_rollback`、`paradigm`、`session_spine` 在装配代码里直接可见，清单里没有；`loop_guard`、`truncation_continue`、`capability_bus` 在我读到的装配代码里看不到。
- 两套并行变体：`NewModeProfileMiddleware` 与 `NewAgenticModeProfileMiddleware`、`NewMemoryMiddleware` 与 `NewAgenticMemoryMiddleware`，而 `BuildTypedMiddlewareStack` 又是泛型（`*schema.Message` 与 `*schema.AgenticMessage`）。
- 路径选择靠会话 ID 前缀：`"agentic:"` 在 12 个文件里各自被剥离（约 25 处；11 个在 `internal/agent`，1 个在 `internal/bridge/context_report.go`）。漏掉一处，会话状态就会分裂。另有 `AGENTGO_TURN_LOOP`（默认关，"run chats through the ADK TurnLoop"，`flags.go:45`）是又一个执行路径开关。
- 注释和实现不符：`Runner` 注释写"ADK ChatModelAgent (preferred) or react fallback"，`Generate` 注释写"single-turn ReAct loop"，`run()` 只走 `runADK`；`AddSteer` 注释写"mid-turn"，实际在回合结束后。
- 系统提示至少 4 处拼接：`runner.go` 的 `defaultSystemPrompt`、`adk_agent_build.go` 的 `Instruction`、`mode.go` 的 `ModeHints`、`toolsearch_reminder.go` 的 `keywordReminder`；另有 AGENTS.md 注入和会话脊柱提醒两处，我没读正文。没有统一的"提示清单"，缓存前缀是否被改动只能靠评测发现。
- `mode.go` 里混着与"模式"无关的开关（`EinoAgentsMDEnabled`、`EinoSkillMWEnabled`、`TurnLoopEnabled` 等）。

### 3.3 同一概念散在多个包

| 概念 | 分布 |
| --- | --- |
| 审批 | `governance`（队列、策略）、`core/approval_flow.go` 与 `approval_relay.go`、`bridge/approval_*`、`agent`（`ContinueAfterApproval`、中断续跑） |
| 后台与定时任务 | `taskhub`、`scheduler`、`trigger`、`agent/bgtask.go`、`tools/job.go`（`kanban` 也是任务类包，没核实是否重叠）。10-28 修过的一个真 bug 就出在这里：任务页读错字段，后台任务列表一直是空的 |
| 会话 | `chat_messages`（界面转录）、`chat_context_events`（模型上下文的真源）、`chat_entries`/`chat_lanes`（没有生产调用方）、`sessions.SessionKernel`、`runtimeSessionStore` |
| 模式 | `ModeProfile × ExecutionCanvas`、`Harness` 预设、`Paradigm`、`Matrix` |
| "harness"这个词 | 代码里是"模式预设"（`agent/harness.go`）；计划文档里是"可被改进的提示与技能"（`docs/plan-harness-evolution.md`）；你问的是第三种意思，整个 agent 框架 |

### 3.4 层表和守卫失去解释力

- L2"Assets"有 22 个包：工具集（`tools`、`codetools`、`codelsp`、`officedoc`、`browsercontrol`、`rekit`、`visionocr`、`docindex`）、扩展资产（`apps`、`agentpack`、`skills`、`workflow`）、调度（`scheduler`、`taskhub`、`trigger`、`kanban`、`terminal`）、评测（`evaluation`、`memeval`、`evolution`）、管理（`admin`）等。内部还有 11 条同层依赖（如 `tools → workflow/skills/apps/docindex`、`agentpack → tools/apps/skills/workflow`），层表表达不了。
- 守卫只管"向上依赖"。同层依赖没有规则（L5 里 `bridge → acp`、`tui → cli`）。
- 入口守卫只覆盖 `cmd/agentgo` 和 `cmd/llmtest`（`TestCMDOnlyImportsBridge`）。`cmd/agentgo-cli` 直接引用 6 个内部包（含 `engine`、`tui`、`cli`），`cmd/smoketest` 是有意的例外，其余入口规则里没写该怎么算。
- `docs/ARCHITECTURE.md` 只有 56 行，产品概念只列了 Tools、Skills、Agents、Workflows、Apps，没有记忆、治理、办公文档、钩子、IM、电脑操作。

### 3.5 死代码与未接线

| 项 | 证据 |
| --- | --- |
| `internal/telemetry` 整包无人导入；它让 `go.mod` 保留了对 `go.opentelemetry.io/otel` 与 `otel/sdk` 的直接依赖。**已删除（2026-10-30）**；`go.mod` 里这两行因此应改成 `// indirect`，没有动，见执行记录 | `go list` 与全库搜索 `InitializeOTel(`，只有定义 |
| `pkg/mcphost` 本仓库内没有任何 Go 代码引用。**这一条我判断错了，它不是死代码**：同级仓库 `agentgo-crush` 的 `cmd/agentgo-mcp` 和 `internal/server` 通过 `mcphost.NewHeadless()`、`mcphost.AppService` 使用它，`docs/plugin-architecture-plan.md` 也写了这条依赖。我只搜了本仓库，没想到 `pkg/` 是给外部用的。保留，并写进了 `docs/ARCHITECTURE.md` | 全库搜索；在 `agentgo-crush` 里搜 `mcphost` |
| `internal/sessions/tree.go` 的 `ForkLane`、`ResetLaneHead` 没有生产调用方（10-28 设计稿 1.2 已指出）。**已删除接口和它的测试（2026-10-30）**；`chat_entries`、`chat_lanes` 两张表保留为遗留空表 | 全库搜索 |
| 仓库根目录的 `entries_test.txt`（96 字节的目录列表，调试残留）被 git 跟踪。**已删除** | `git ls-files` |

### 3.6 行为由隐式开关和默认值决定

- `internal/config/flags.go` 里有约 100 个 `AGENTGO_*` 开关，其中一些改变核心行为（`AGENTGO_EXECUTION_CANVAS`、`AGENTGO_ARMOR_BREAK`、`AGENTGO_TOOL_BARRIER`）。
- 一个会话最终有多大权限，由审批策略、模式 × 画布、破甲、记住的批准叠加决定，生效点分散在 `mode.go`（`ArmorBreakActiveFor`）、`runner.go`（`effectivePolicyForCtx`）、`armor_nudge.go`。"逆向"预设默认开启破甲：开放控制加会话级自动批准（`mode.go` 注释写明是 2026-10-30 的决定）。这是有意的产品决定，问题不在决定本身，而在我没找到集中展示"这个会话为什么不再弹审批"的地方（没有核实界面，不排除已有）。
- 10-28 的端到端曾因为没设 `AGENTGO_EXECUTION_CANVAS=deep` 而一度失败，说明测试对隐式配置敏感。

### 3.7 供应链、构建与历史

- 运行时核心依赖 eino 的 alpha 版本，并 `replace` 到个人 fork（`go.mod` 末尾注释："Runtime backports pending upstream PR #1401"）；CI 要能访问该仓库。Wails 也是 alpha（`v3.0.0-alpha.97`）。
- 第二份依赖清单 `go386.mod`（32 位 Windows），靠 `replace github.com/bytedance/sonic => ./third_party/sonic-std` 绕过 sonic 在 32 位上无法编译的问题；两份清单要手工保持同步。
- 当前 HEAD `e21059c`（2026-10-30，"sync win7 development tree to GitHub"）一个提交改了 1,031 个文件、+139,552/−4,304 行，全库共 83 个提交。对回溯和审查不友好；原因我不知道，可能与公开发布前清理历史有关（`docs/public-remote-history-audit-2026-10-24.json`）。
- `docs/` 跟踪了 88 个文件，绝大多数是带日期的施工记录；"当前架构是什么"没有一份跟得上的文档。

## 4. 建议顺序

工作量只是估计：S 小于一天，M 一到三天，L 一周以上。

| 优先级 | 事项 | 工作量 | 为什么 |
| --- | --- | --- | --- |
| P0 | 修"回滚检查点"：把按钮文案改成"拒绝本会话全部 AI 修改"，或把轮次真正传给后端 | S | 现在文案承诺的和实际做的不一样，用户会误以为能回到某一轮 |
| P0 | 重写 `docs/ARCHITECTURE.md` 为当前态（7 层、core 与 bridge 的真实职责、产品概念补全），并更正 10-28 差距表的 7 处 | S | 后续所有判断都以它为准；现在它会误导新加入的人和模型 |
| P0 | 清死代码：`internal/telemetry`（要么接线要么删）、`sessions/tree.go` 的 lane 接口、根目录 `entries_test.txt`（`pkg/mcphost` 不是死代码，见 3.5） | S | 删除没有风险，能少一个"看似有、其实没有"的误判来源 |
| P0 | `MiddlewareNames()` 改为由装配代码生成，或直接删除 | S | 手写清单已经失真，比没有更糟 |
| P0 | 发布流程加一个 `-tags web` 的 `agentgo-cli` 跨平台构建 | S | linux 目标已真实编译通过（见 3.1）；macOS 目标没试；解锁 Linux/macOS 的 CLI |
| P1 | 纠偏改为下一次模型调用前注入 | S–M | 现在的"中途纠偏"实际是"回合后纠偏" |
| P1 | 撤销的基线建立下沉到 `sendMessageCore` | M | 现在撤销能力取决于从哪个入口进来 |
| P1 | 模型档案一张表 + 辅助模型可选配置 | M | 收口散落的分支，也是评测矩阵的前提 |
| P1 | 守卫补全：所有 `cmd/*` 的入口规则、L5 同层规则 | S–M | 现在新增入口不受约束 |
| P2 | 把 `bridge` 里的业务编排逐个下沉到 `core`，顺序：`approval_resume` → `running_work` → `sendMessageCore`；每次迁移前后跑同一套测试；目标"`bridge` 直接依赖的包数降到 30 以内" | L | 这是让层模型重新有解释力的唯一办法；不要一次性迁 |
| P2 | Windows 命令沙箱（受限令牌 + 低完整性级别），要如实写明做不到网络隔离。**已做（2026-10-30 夜），见 7.5** | L | 差距表第 1 项 |
| P2 | 评测扩到 100 例以上并跑模型矩阵；在此基础上再推 `plan-harness-evolution.md` 的第 3–6 步 | L | 评测集不够，自我改进循环只会过拟合 |
| P2 | 子智能体运行表（登记、查看、停止、恢复） | M–L | 运行中视图要它，撤销按轮次也要它 |

**状态（2026-10-30）**：上表五个 P0 已全部完成，做法与验证见第 7 节；"清死代码"的范围比原设想小（`pkg/mcphost` 保留）。夜里又做了三件：Windows 命令沙箱（P2）、按轮回退（原 P1“按轮次基线”的内存版）、单独 tidy `go.mod`，见 7.5。P1 里“撤销的基线建立下沉到 `sendMessageCore`”仍没做。

## 5. 没有验证的

- 竞品一侧沿用 10-28，没有重新核对。
- `agentgo-crush` 还用了 `bridge.AppService` 的哪些方法，没有逐个核对；只确认了它依赖 `pkg/mcphost`，并且没有调用被删的 `GetMiddlewareStack`、`MiddlewareNames`。
- `.github/workflows/` 的两处新增（`ci.yml` 的 `go-portable` 多一步、`release.yml` 新增 `cli-portable`）没有在 GitHub 上跑过，只做了 YAML 语法检查和等价的本地交叉编译（含按"git 会跟踪的文件"拷出的干净副本）。而且 `gh run list` 显示这个仓库在 GitHub 上只有一条 2026-06-08 的依赖图运行记录：`CI`、`Release` 两个工作流本身没有任何运行记录，已有的 `go-windows`、`go-portable` 也没有实测过。
- `go list` 用的是默认构建标签，`-tags web` 的文件（`webshell`、`cmd/agentgo-web`）没有计入依赖数字。
- `suite_default.json` 等的条目数沿用 10-28 的 20/3/9，没有重数。
- `toolExecutionHandlers`（决定工具层中间件顺序）没有读，所以第 3.2 节的管线对照是不完整的。
- 前端行为：第一轮（白天）没有在浏览器里验证，"回滚检查点"和"撤销只在前端入口建基线"的结论来自读代码。夜里的按轮回退界面在浏览器预览（mock 后端）里走通了，**Wails 桌面窗口里没有跑过**，见 7.5。
- 模型家族名的搜索是粗筛，只读了其中两处。
- "`core` 被架空"来自依赖数据加职责阅读，没有统计 `bridge` 里业务逻辑的行数。
- 没有核实 `browsercontrol` 是何时加入的；只确认它现在存在。

## 6. 复现

```powershell
# 包依赖：与 internal/arch/layers.go 的 PackageLayer 对照，得到层分布、向上边、同层边、fan-in/out
go list -f '{{.ImportPath}}|{{len .GoFiles}}|{{join .Imports ","}}' ./internal/... ./cmd/... ./pkg/...

# AppService 导出方法数（非测试文件）
Get-ChildItem internal\bridge -Filter *.go | Where-Object { $_.Name -notlike '*_test.go' } | Select-String -Pattern '^func \(\w+ \*AppService\) [A-Z]' | Measure-Object

# 谁调用了 BeginWorkspaceReview / DrainSteers / InitializeOTel
rg "BeginWorkspaceReview|DrainSteers\(|InitializeOTel\(" --glob '!*_test.go'

# CLI 是否能脱离 Wails（linux 目标）
$env:GOOS='linux'; $env:CGO_ENABLED='0'; go list -deps -tags web ./cmd/agentgo-cli | Select-String wailsapp
```

## 7. 执行记录（2026-10-30 晚）

第 4 节的五个 P0 按"改一项、验一项"做完。没有提交，所有改动都在工作区里。

### 7.1 改了什么

| 事项 | 改动 | 与原设想的差别 |
| --- | --- | --- |
| 回滚检查点 | `frontend/src/features/chat/FlowChatTranscript.tsx`：按钮改成"↶ 撤销未接受改动"，确认框如实写明范围：本会话全部未接受的改动、新建的文件会被删除、这期间你手动做的修改也会撤销、已接受的不受影响；函数改名 `discardUnacceptedChanges`；后端没动 | 没有做按轮回退。基线是会话级的（`ReviewStore.Begin` 在已有未处理改动时保留最初的基线），按轮回退要每轮存快照，留在 P1 |
| 文档 | 重写 `docs/ARCHITECTURE.md`；10-28 差距表 7 处加"更正（2026-10-30）"；另两份旧文档里提到 `tree.go` 的句子加了更新说明 | 旧文保留原样，只加标注 |
| 死代码 | 删除 `internal/telemetry`（2 个文件）、`internal/sessions/tree.go` 与 `tree_test.go`、根目录 `entries_test.txt`；`internal/arch/layers.go` 去掉 `telemetry` 登记；`delete_integrity_test.go` 改成直接用 SQL 造旧表数据；`store.go` 给两张遗留表加了说明注释 | **`pkg/mcphost` 没删**（是 `agentgo-crush` 的入口）；**两张表没删**（旧库外键和删会话时的清理）；**`go.mod` 没动**（见 7.3） |
| `MiddlewareNames()` | 删除 `agent.MiddlewareNames` 与 `AppService.GetMiddlewareStack`（前端、IPC 契约、测试、`agentgo-crush` 里都没有调用方）；`app_ipc.go` 去掉不再使用的 `agent` 导入；`AppService` 导出方法 295 → 294 | 选了"删"而不是"生成"：没有消费者；真实顺序写进了 `docs/ARCHITECTURE.md` 第 7 节 |
| CLI 跨平台发布 | `release.yml` 新增 `cli-portable`（linux amd64/arm64、darwin arm64/amd64，`CGO_ENABLED=0`，`-tags web`）；`ci.yml` 的 `go-portable`（ubuntu 与 macOS）多一步编译 `agentgo-cli` | 比设想多了 linux/arm64 和 darwin/amd64。**`release.yml` 里的 `cli-portable` 之后撤掉了**（2026-10-31 起只发布 Windows 版）；`ci.yml` 的 `go-portable` 保留 |

### 7.2 验证

| 检查 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go build -tags web ./cmd/agentgo-web ./cmd/agentgo-cli` | 通过 |
| `go vet ./...` | 通过（全仓） |
| `go test -count=1`：`sessions`、`arch`、`workspace`、`core`、`agent`、`bridge` | 全部 ok（`agent` 154 秒，`bridge` 48 秒） |
| 前端 `npm run typecheck`、`npm test` | 通过；106 个测试，0 个失败 |
| `agentgo-cli` 交叉编译（`CGO_ENABLED=0 -tags web -trimpath -ldflags "-s -w"`），本机工作区 | linux/amd64 50.9 MB、linux/arm64 47.2 MB、darwin/arm64 48.9 MB、darwin/amd64 52.3 MB，全部通过。macOS 目标这是第一次实测 |
| 同上，在按"git 会跟踪的文件"拷出的干净副本里（`frontend/dist` 只有 `.gitkeep`，没有 `node_modules`） | 4 个目标全部通过，产物各小 0.7 到 0.8 MB |
| 两个工作流文件 | YAML 语法解析通过（PyYAML）；没有在 GitHub 上跑过 |

干净副本的产物偏小，是因为本机有一部分文件被 git 有意忽略、不入库：一个编进二进制的内置模板（注释写明"编进二进制但不入库"），以及私有扩展的源码。其余差值应来自这些私有文件，我没有逐项量。这是原有的设计，不是这次引入的：CI 构建的二进制不带这些文件，不影响编译。

### 7.3 没做的和需要你决定的

- **（已于 2026-10-30 夜完成，见 7.5）`go.mod` / `go386.mod` 没有整理。** 删掉 `telemetry` 之后，`go.opentelemetry.io/otel` 与 `otel/sdk` 应该从直接依赖变成 `// indirect`。我用 `go mod tidy -diff` 预览过：`go.mod` 本来就有一批与这次无关的整理项（未使用的 `eino-ext/components/tool/bingsearch`、应为直接依赖的 `golang.org/x/{crypto,image,net}` 和 `skip2/go-qrcode` 等），直接运行会把它们和 `go.sum` 的变动一起混进来。建议单独做一次 tidy 提交；`go386.mod` 要用 `go -modfile=go386.mod mod tidy`（我预览时 `-modfile` 的位置写错了，那份没有预览成功）。
- **（已于 2026-10-30 夜完成，见 7.5）没有做按轮回退。** 这是差距表第 3 项里剩下的部分，要先定快照放内存还是落盘，以及怎么和对话上下文联动。
- **工作流没有在 GitHub 上跑过**，见第 5 节。第一次推送或打 tag 时，要看一眼 `cli-portable` 的结果。（2026-10-31 更新：`ci.yml` 已在 GitHub 上跑过并通过；`cli-portable` 已随“只发布 Windows 版”从 `release.yml` 撤掉。）
- `gofmt -l` 在 `internal/arch` 和 `internal/sessions` 里列出 12 个文件（`export.go`、`fork.go`、`titles.go`、`process_spawn_test.go` 等），都不是我这次改的，应是本来就有的格式或换行问题；CI 的 gofmt 步骤只检查 PR 里新增或修改的文件，所以它们在被改到时才会被拦。

### 7.4 回退办法

`git diff --stat` 确认过：本次动过的 12 个已有文件里，除了这次的改动没有别的未提交修改，被删的 5 个文件在删除前与 HEAD 一致。所以可以逐文件恢复：

```powershell
# 恢复被删的文件
git restore -- internal/telemetry internal/sessions/tree.go internal/sessions/tree_test.go entries_test.txt
# 其余文件按 7.1 节的清单逐个 git restore；恢复前先确认它们之后没有新的未提交改动
```

## 7.5 第二轮执行记录（2026-10-30 夜）

7.3 留下的三项（整理 `go.mod`、按轮回退、Windows 命令沙箱）按 `docs/SYSTEM_DESIGN_2026-10-30.md` 做完。设计、施工偏离和逐项验证都在那份文档里（第 6.1、6.2 节），这里只记结果。没有提交，所有改动都在工作区里。

### 7.5.1 改了什么

| 事项 | 改动 | 要点 |
| --- | --- | --- |
| 整理 `go.mod` | `go.mod`、`go.sum`、`go386.mod`、`go386.sum`，单独做，没有混进别的改动 | 去掉没用的 `eino-ext/.../bingsearch`；`go-qrcode`、`x/crypto`、`x/image`、`x/net` 升为直接依赖；`otel`、`otel/sdk` 降为 `// indirect`（删 `telemetry` 的后续）；`go386.mod` 另外去掉 5 个 32 位用不到的间接依赖。命令是 `go mod tidy "-modfile=go386.mod"`（PowerShell 里参数要加引号，上一轮写的 `go -modfile=… mod tidy` 顺序是错的） |
| 按轮回退（后端） | `internal/workspace/checkpoint*.go`（检查点存储、预览、回退）、`internal/core/turnmeta.go`、`internal/engine`（`StartedExtra`）、`internal/bridge/workspace_checkpoint.go` 与 `engine_ipc.go`、`config.TurnCheckpoints` | 每个经 `runEngineTurn` 的轮次开始前建一份内存检查点；三个 IPC：`ListTurnCheckpoints`、`PreviewTurnRollback`、`RollbackToTurn`；回退前自动留一份可撤销的安全检查点；开关 `AGENTGO_CHECKPOINTS`，默认开 |
| 按轮回退（前端） | `turnCheckpointModel.ts`、`useTurnRollback.ts`、`FlowChatTranscript.tsx`、IPC 契约、浏览器预览的 mock、样式 | 每条带检查点的用户消息有“↩ 回到这一轮之前”，先预览再确认；回退后出现说明分隔线和“撤销这次回退”条 |
| Windows 命令沙箱 | `internal/sandbox/os_windows.go`、`os_windows_token.go`、`os_windows_label.go`；`os_other.go` 的构建标签；`flags.go` 的开关说明；`shell_run.go` 的报错文案 | 只防写，不防读、不防网络；默认配置拒绝执行，必须设 `AGENTGO_SANDBOX_OS_NET=1` 表示接受；工作区被打上永久的低完整性标签 |
| 顺带修的老 bug | `FlowChatTranscript.tsx` | 它自己调 `useChat()`（不是单例），拿到的是空会话，所以白天改文案的“↶ 撤销未接受改动”点了没有任何效果；现在用 `AppShell` 注入的上下文 |

### 7.5.2 验证

| 检查 | 结果 |
| --- | --- |
| `go build ./...`、`go vet ./...` | 通过 |
| `go test -count=1`：`sandbox`、`shellcmd`、`tools`、`config`、`arch`、`bridge`、`workspace`、`core`、`engine`、`sessions`、`agent` | 全部 ok |
| Windows 沙箱实机测试 | `sandbox` 13 个加 `tools` 1 个端到端，无一跳过；覆盖：能改工作区里已有的、新建的、深层的文件并删文件（含孙进程）；写工作区外、`%TEMP%`、HKCU 注册表被拒；令牌是低完整性且只剩 `SeChangeNotifyPrivilege`；标签不顺着目录联接扩散；标签被拒、目录过大、过宽、相对路径都拒绝执行；默认配置拒绝执行；超时能连子进程一起结束 |
| 变异检查 | 临时去掉套令牌那一行，4 个测试按预期失败，已还原 |
| 32 位 | 同一批沙箱测试以 `GOARCH=386` 在这台 64 位 Windows 11 上跑通 |
| 构建矩阵 | `-tags web`（windows/amd64）；386（普通与 Win7 旧工具链）的 `./internal/... ./cmd/agentgo-web/...`；`agentgo-cli`、`agentgo-cua` 的 linux/amd64、linux/arm64、darwin/amd64、darwin/arm64、windows/arm64；`go vet` 在 linux、darwin、freebsd 上 |
| 前端 | `npm run typecheck`、`npm test`（132 个，0 失败）、`npm run build` 通过 |
| 回退界面 | 浏览器预览（mock 后端）里端到端走通，包括撤销条跨 `reload` 保留、撤销、撤销后的说明 |

### 7.5.3 没验证的和需要知道的

- **Wails 桌面窗口里的回退界面没有跑过。** “真实后端加真实界面”整链没有手动走过；两头各自有测试（Go 的 `bridge` 测试、浏览器里 mock 后端的端到端）。
- **Win7 实机没有跑**，只验证了 32 位版本在 Windows 11 的 WOW64 里通过。
- **Linux、macOS 沙箱没有动，也仍没在真机上运行**；只验证了新的构建标签没有弄坏它们。
- **Windows 沙箱是“防误伤”，不是“防泄密”**：读取和网络都不受限。它因此默认拒绝执行，要用户设 `AGENTGO_SANDBOX_OS_NET=1` 表示知情。
- **标签是永久的**：AgentGo 不会撤销；手动撤销用 `icacls <目录> /setintegritylevel M /T`。放在非用户目录的项目要先给自己“完全控制”，报错里有现成的 `icacls` 命令。
- **沙箱内的程序写不了自己的缓存目录**（`GOCACHE`、npm、pip 等），构建类命令可能因此失败；只重定向了 `TEMP`、`TMP`。
- **检查点只在内存里**，重启即丢；ACP、终端“快速修复”、工作流的 `codingAgentRun` 不经 `runEngineTurn`，不建检查点；限额（512 MiB、30 个、20 万文件、20 秒、暂停 10 分钟）是拍的数，没有用大工作区实测。
- **提权运行时的沙箱行为没有测**：这台机器上的令牌本来就被 UAC 过滤过，测试只能证实“只剩 `SeChangeNotifyPrivilege`”。

### 7.5.4 回退办法

和 7.4 一样逐文件：新增文件直接删除，被改的文件用 `git restore` 前先确认它们之后没有新的未提交改动。**不要对整个工作区执行 `git restore .` 或 `git checkout -- .`**：白天的改动和这一轮混在同一个未提交的工作区里。沙箱打过的标签不在 git 里，要另外用 `icacls` 撤销（见上）。
