# AgentGo 架构（当前态，2026-10-30）

这份文档描述代码现在的样子，以及哪些规则有测试守着。设计意图和现实不一致的地方直接写出来。

证据分两级：标"守卫"的由 `internal/arch` 的测试保证，改坏了测试会失败；标"读代码"的是人读出来的，没有测试守着，改动之后可能失真。

## 1. 一句话

AgentGo 是 Go 写的智能体应用。桌面窗口（Wails）、浏览器版、终端客户端、HTTP 网关、ACP、IM 渠道，都经 `internal/bridge` 装配出的运行时驱动同一套智能体：用 cloudwego/eino 的 ADK（`ChatModelAgent` 加中间件管线）调模型和工具，会话存在本地 SQLite。

## 2. 七层依赖模型（守卫）

依赖只能向下：高层可以 import 同层或更低层，不能反过来。登记表是 `internal/arch/layers.go` 的 `PackageLayer`，共 81 项（69 个顶层包加 12 个子包）。新增 `internal/<pkg>` 必须登记，否则 `go test ./internal/arch/...` 失败。

| 层 | 项数 | 放什么 | 成员 |
| --- | --- | --- | --- |
| L0 Foundation | 30 | 运行底座和不依赖业务的小部件 | 数据与配置：`db`、`sessions`、`workspace`、`config`（`AGENTGO_*` 设置的唯一读取点）、`event`、`applog`、`buildinfo`。平台与安全：`processutil`、`shellcmd`、`textenc`、`redact`、`pathgrant`、`hostui`、`computeruse`。交付与证据：`artifact`、`evidence`、`promptbudget`。插件宿主：`plugin`。IM 渠道适配器：`imchannel` 及其子包（钉钉、Discord、飞书、Telegram、企业微信、微信）。元信息：`arch`。测试辅助：`linktest`（给测试造"同一个文件夹的另一种写法"：链接、Windows 8.3 短名；只被测试文件引用） |
| L1 Systems | 22 | 治理和跨功能系统 | `governance`（审批队列、策略、审计）、`memory`、`capability`、`checkpoint`、`gateway`、`channels`、`im`、`interactive`、`externalcontent`、`sandbox`、`coderuntime`、`compose` 及其 3 个子包、`ledger`、`spill`、`ideruntime`、`userhooks`、`docchunk`、`engine`、`audio` |
| L2 Assets | 22 | 工具和可扩展的资产 | 工具：`tools`、`codetools`、`codelsp`、`powernaplsp`、`officedoc`、`browsercontrol`、`visionocr`、`docindex`、`rekit`。扩展：`skills`、`workflow`、`apps`、`agentpack`。调度与任务：`scheduler`、`taskhub`、`trigger`、`kanban`、`terminal`。评测与演进：`evaluation`、`memeval`、`evolution`。管理：`admin` |
| L3 Modes | 1 | 智能体编排 | `agent`：模式、`Runner`、ADK 装配、中间件 |
| L4 Kernel | 1 | 设计意图：所有入口共享的业务逻辑 | `core`（现实见第 8 节） |
| L5 Consumer | 5 | 协议适配和运行时装配 | `bridge`（Wails IPC 加 `Runtime` 装配）、`acp`、`tui`、`cli`、`webshell` |
| L6 CMD | - | 入口程序 | `cmd/*`，见第 4 节 |

## 3. 守卫测试管什么、不管什么

管（`internal/arch`）：

- 每个 `internal/*` 包都登记过（`TestEveryInternalPackageRegistered`），没有向上依赖（`TestNoUpwardInternalImports`），`arch` 自己只依赖标准库。
- 三条硬规则（`TestHardRules`）：
  1. 除 `bridge` 自己外，任何包不得 import `bridge`。
  2. `apps`、`workflow`、`tools` 不得 import `agent`。
  3. `agent` 不得 import `bridge`（由第 1 条覆盖）。
- `cmd/agentgo` 与 `cmd/llmtest` 只能 import `bridge` 和 `config`（`TestCMDOnlyImportsBridge`）。`cmd/smoketest` 明确例外。
- 插件规则（`plugin_rules_test.go`）：具体插件只能经装配层到达；宿主和契约包只依赖标准库；插件构造函数和运行时装配不得依赖 `AppService`。
- `AGENTGO_*` 环境变量只能出现在 `internal/config`（`env_rules_test.go`）；每个子进程启动都要隐藏控制台窗口（`process_spawn_test.go`）；Crush 不得被导入（`crush_freeze_test.go`）。

不管：

- 同层依赖（例如 L5 里 `bridge` 依赖 `acp`、`tui` 依赖 `cli`）。
- `cmd/agentgo`、`cmd/llmtest` 之外的入口（`agentgo-cli`、`agentgo-web`、`agentgo-cua`、`agentgo-eval`、`agentgo-re`）该依赖哪些包，没有规则。
- "业务逻辑该放哪一层"：规则只限制方向，不限制职责。

## 4. 入口

| 入口 | 是什么 |
| --- | --- |
| `cmd/agentgo` | 桌面应用（Wails 窗口） |
| `cmd/agentgo-web` | 不开原生窗口的浏览器版：在本机回环端口提供界面并用系统浏览器打开，Win7 构建走它；必须带 `-tags web` |
| `cmd/agentgo-cli` | 终端客户端：在本进程内装配运行时（`bridgeBackend`），不连桌面应用；带 `-tags web` 时不依赖 Wails |
| `cmd/agentgo-cua` | 电脑操作 sidecar：截屏和输入注入，经仅属主可访问的命名管道提供服务 |
| `cmd/agentgo-eval` | 对运行中的 `agentgo --serve` 跑固定任务套件，与基线比较通过率、token、缓存命中 |
| `cmd/agentgo-re` | 自带的逆向工程 MCP 服务端（stdio） |
| `cmd/llmtest` | 用 AgentGo 自己的客户端代码检查一个 OpenAI 兼容端点 |
| `cmd/smoketest` | 本地冒烟驱动，允许直连更深的包 |

仓库外还有一个入口：`pkg/mcphost`（唯一的 `pkg/` 包）。同级仓库 `agentgo-crush` 的 `cmd/agentgo-mcp` 通过 `mcphost.NewHeadless()` 起一个无界面的 `AppService`。所以本仓库里没有任何 Go 代码引用它是正常的，不是死代码；改动 `bridge.AppService` 的导出方法时，要同时看前端、终端客户端和 `agentgo-crush`。

## 5. 一次对话怎么走（读代码，没有逐跳验证）

`AppService.SendMessageWithSession`（`internal/bridge/app_ipc.go`）→ `runEngineTurn(engine.RunRequest{...})`（`internal/bridge`）→ `agent.Runner`（`internal/agent/runner.go`，只走 ADK 路径）→ ADK `ChatModelAgent`（`adk_agent_build.go` 装配）→ 中间件管线与工具。事件按会话分流回到界面；转录、模型上下文、运行事件各自落库，见第 6 节。

## 6. 会话存储

| 表 | 作用 |
| --- | --- |
| `chat_sessions`、`chat_messages` | 界面看到的转录 |
| `chat_context_events` | 模型上下文的真源（`docs/SYSTEM_DESIGN_2026-10-28.md`） |
| `chat_run_events`、`pending_runs` | 运行事件、等待审批的运行 |
| `session_modes` | 每个会话的模式 |
| `chat_session_forks` | 分叉谱系 |
| `chat_entries`、`chat_lanes` | **遗留空表，没有写入方。** 2026-10-30 删除了无调用方的 `sessions/tree.go`，两张表只为旧库的外键和删会话时的清理保留，等确认没有库里还有数据再用迁移删除 |

工作区改动的审阅基线（`internal/workspace/review.go`）不在库里：每个会话一份内存快照，重启即丢；从开始跟踪起算，只在用户接受或撤销时推进，不是按轮次的。按轮次的是另一套检查点，同样只在内存里，见第 11 节。

## 7. 中间件管线（读代码）

没有单一清单。顺序由三处代码共同决定：

- `handlers.go` 的 `BuildADKHandlers`：把各处理器装进 ADK。
- `middlewares.go` 的 `BuildTypedMiddlewareStack[M]`（泛型，同时服务 `*schema.Message` 与 `*schema.AgenticMessage`），源码里编号 1–8：1 补全被挂起的工具调用；2 摘要（Eino 官方中间件，阈值取自执行画布）；3 AGENTS.md 注入；4 技能注入；5 后台任务控制（`task_output`、`task_stop`）；6 计划任务（`TaskCreate` 等）；7 溢出（大输出落盘并留预览）；8 裁剪（清理与可选截断）。
- 工具执行层的处理器（`toolExecutionHandlers`）和 `tool_barrier.go`：同一轮回复里有改文件或没声明特性的工具调用时，按顺序执行，只并行声明了只读且低风险的调用。

2026-10-30 删除了手写的 `MiddlewareNames()`（以及前端没人调用的 `GetMiddlewareStack`）：它和真实管线已经对不上，比没有更糟。要看真实顺序，读上面三处装配代码。

## 8. 与层模型不一致的地方

层模型规定了方向，现实在职责上偏离了设计意图（数字为 2026-10-30 实测，之后会变）：

- **`bridge` 实际是内核。** 113 个文件、约 1.1 万行，直接依赖 68 个其他顶层包里的 47 个，`AppService` 有约 295 个导出方法。运行一轮对话的编排（`sendMessageCore`）、运行中视图、批准后续跑、会话分叉、钩子接线都在这里。设计上作为内核的 `core` 只有 18 个非测试文件、约 2,700 行，唯一调用方是 `bridge`。
- **`agent` 是大包。** 84 个文件、约 1.2 万行，`Runner` 有 25 个字段。会话 ID 前缀 `"agentic:"` 在 12 个文件里各自被剥离，选择走哪套中间件变体。
- **同一概念分散。** 审批在 4 个包，后台与定时任务在 5 处，会话有 5 种结构。
- **落点规则（设计意图，没有测试守着）：** 新的业务编排放 `core` 或更低层，`bridge` 只放 IPC 适配和运行时装配。迁移顺序和度量见 `docs/architecture-clarity-and-harness-gap-2026-10-30.md` 第 4 节。

## 9. 构建形态

- 默认标签：桌面应用。`internal/bridge/host_desktop.go`（`//go:build !web`）是 `internal` 里唯一直接引用 Wails 的文件。
- `-tags web`：去掉 Wails，`CGO_ENABLED=0` 即可构建。`cmd/agentgo-web` 必须带它；`cmd/agentgo-cli` 带它之后没有 Wails 依赖，可以交叉编译到 Linux 和 macOS（CI 与发布流程见 `.github/workflows/`）。
- Win7 与 32 位：第二份依赖清单 `go386.mod`，靠 `replace github.com/bytedance/sonic => ./third_party/sonic-std` 绕过 sonic 在 32 位上无法编译的问题。两份清单要手工同步。
- 前端：`frontend/`，`npm run build` 产出 `frontend/dist`（被 gitignore，`go:embed` 需要它先存在）。
- 依赖风险：eino 是 alpha 版并 `replace` 到个人 fork，Wails 是 v3 alpha。

## 10. 五大产品概念与其他能力

| 概念 | 包 |
| --- | --- |
| Tools | `internal/tools`、`codetools`、`codelsp`、`officedoc`、`browsercontrol`、`rekit` |
| Skills | `internal/skills` |
| Agents | `internal/agent` 加子智能体注册表（`subagent_registry`） |
| Workflows | `internal/workflow` |
| Apps | `internal/apps` |

另有：记忆 `internal/memory`；治理 `internal/governance`；用户钩子 `internal/userhooks`（`hooks.json`，围绕工具调用和消息运行，见 `docs/user-hooks.md`）；IM `internal/im` 与 `internal/imchannel`；电脑操作 `internal/computeruse` 与 `cmd/agentgo-cua`；命令沙箱 `internal/sandbox`（默认关，三个平台的约束不同，见第 12 节）；评测 `internal/evaluation` 与 `cmd/agentgo-eval`。

## 11. 按轮回退（检查点）

证据：Go 测试（`internal/workspace`、`internal/bridge`、`internal/core`、`internal/engine`）和前端模型测试；界面流程在浏览器预览（mock 后端）里走通，**没有在 Wails 桌面窗口里跑过**。

每个用户轮次开始前，`runEngineTurn`（`internal/bridge/engine_ipc.go`）给工作区拍一份检查点（`internal/workspace/checkpoint*.go` 的 `CheckpointStore`）：每个文件的路径、sha256 和（装得下时）内容。内容按哈希存一份、各检查点共享，所以每多一轮只多存被改过的文件。界面上每条带检查点的用户消息有“↩ 回到这一轮之前”：先预览（`PreviewTurnRollback`，只读），确认后回退（`RollbackToTurn`）；回退前自动留一份 `pre_rollback` 检查点，界面给出“撤销这次回退”。

- **怎么和消息对上**：检查点 ID（`cp_<毫秒>_<6 位十六进制>`）写进用户消息的 `meta.checkpoint`（重载会话后仍在），实时那一轮由 `started` 状态事件的 `checkpoint_id` 带给前端（`RunRequest.StartedExtra`）。没有 ID 的消息不显示按钮。
- **只动文件，不删对话**：回退后追加一条说明消息（`meta.kind = workspace_rollback`），模型下一轮看得到，界面画成分隔线。
- **不做无法撤销的事**：三种文件会被跳过并在预览里列出，其余照常还原：原始内容没存下来的（超过 8 MiB，或内存预算用完了）、现在的内容太大没法留安全副本的（覆盖或删除后撤销不回来）、建检查点时读不出来的。被忽略的目录（`.git`、`node_modules` 等）和不是普通文件的（符号链接、设备）根本不在检查点里，回退也不碰它们。
- **运行中拒绝回退**：有运行或待审批时（`rollbackBusyReason`）。
- **限额**：单文件 8 MiB、每会话 128 MiB、全局 512 MiB、每会话 30 个检查点、20 万个文件、一次扫描 20 秒。扫描太慢或文件太多时，这个工作区暂停建检查点 10 分钟，对话照常进行。
- **开关与寿命**：`AGENTGO_CHECKPOINTS`（默认开）。全部在内存，重启即丢；丢了之后旧消息的按钮不显示。
- **没覆盖的**：ACP、终端“快速修复”、工作流里的 `codingAgentRun` 不经 `runEngineTurn`，不建检查点。检查点靠 size 加 mtime 判断文件有没有变，2 秒内改过的文件会重新读内容；更早之前被人为伪造了 size 和 mtime 的改动看不出来。
- **前端**：`frontend/src/stores/turnCheckpointModel.ts`（纯逻辑，有测试）、`features/chat/useTurnRollback.ts`、`FlowChatTranscript.tsx`。

设计与施工偏离见 `docs/SYSTEM_DESIGN_2026-10-30.md`。

## 12. 命令沙箱（`internal/sandbox`）

证据：Windows 一行是实机测试；Linux、macOS 两行是读代码加交叉编译，**没有在真机运行过**。

`AGENTGO_SANDBOX_OS=1` 打开，只作用于 `execute_bash`（`internal/tools/shell_run.go`），默认关。本机不支持或准备失败时拒绝执行，不会悄悄无约束运行。

| 平台 | 机制 | 实际约束 |
| --- | --- | --- |
| Linux | Landlock + seccomp，自重新执行垫片（`os_linux.go`） | 只能写工作区和临时目录；读限于系统目录、工作区、临时目录和 `AGENTGO_SANDBOX_OS_READ`（`$HOME` 下的密钥目录读不到）；默认无 IP 网络 |
| macOS | Seatbelt（`os_darwin.go`） | 同 Linux 的策略 |
| Windows | 受限令牌（去掉特权，管理员组降为仅拒绝）加低完整性级别加目录标签（`os_windows*.go`） | **只管写**：只能写工作区子树和私有临时目录（`%LOCALAPPDATA%\agentgo\sandbox-tmp`，命令的 `TEMP`、`TMP` 指向这里）；写别处、`%TEMP%`、HKCU 注册表被 Windows 拒绝。**读取不受限，网络不受限** |
| 其他 | 无 | `Command` 返回 `ErrUnsupported` |

Windows 要注意的几点：

- 因为挡不住网络和读取，默认配置（没设 `AGENTGO_SANDBOX_OS_NET=1`）直接拒绝执行；设了就表示“我知道它只防误写，不防泄密”。`AGENTGO_SANDBOX_OS_READ` 在 Windows 上被忽略。
- 工作区目录会被打上**永久**的低完整性标签：一次 `SetNamedSecurityInfo`，系统自动传给已有文件和之后新建的文件（实测不会顺着目录联接扩散到工作区外）。AgentGo 不会自动撤销，手动撤销用 `icacls <目录> /setintegritylevel M /T`。标签残留意味着其他低完整性进程，以及之前启用过沙箱的别的工作区里的沙箱命令，也能写这些目录。
- 打标签需要对目录有“更改所有者”权限：用户目录下的项目默认有，其他盘常常没有，这时拒绝执行并在报错里给出 `icacls ... /grant` 命令。盘根、用户目录及其上级、Windows 与 Program Files 目录、非 NTFS、网络盘、超过 40 万个文件和文件夹的目录一律拒绝。
- 沙箱内的程序写不了自己的缓存目录（`GOCACHE`、npm、pip 的缓存都在用户目录里），构建类命令可能因此失败；只重定向了 `TEMP`、`TMP`。
- Win7 与 32 位：32 位版本在 64 位 Windows 11 上跑过同一批测试；Win7 实机没有验证。

## 相关文档

- 按轮回退与 Windows 沙箱的设计、施工偏离和验证记录：`docs/SYSTEM_DESIGN_2026-10-30.md`
- 差距与架构清晰度的完整核实：`docs/architecture-clarity-and-harness-gap-2026-10-30.md`
- 钩子、会话分叉、运行中视图的设计与施工记录：`docs/SYSTEM_DESIGN_2026-10-28.md`
- 插件架构：`docs/kernel-plugin-consensus.md`、`docs/plugin-architecture-plan.md`
- 施工计划：`docs/plan.md`

## 本地快速检查

```powershell
go test ./internal/arch/... ./internal/apps/... -count=1
```
