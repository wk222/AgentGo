# AgentGo 施工计划（2026-06）

## 北极星（一条故事，三个月不换）

**桌面里用自然语言做出可打开的 Inner App，并能稳定调用工作流 / 对话 API。**

验收：用户说需求 → `build_inner_app_iteratively` 或侧栏新建 → 应用面板可打开 → `ping` 或 `chat` 或 `workflow_run` 至少一种可用。

---

## 架构原则（不对齐 PyBot 全文，只守三条）

| # | 规则 |
|---|------|
| 1 | 除 `bridge` 外，任何 `internal/*` **不得 import `bridge`** |
| 2 | **资产**（`apps` / `workflow` / `tools`）**不得 import `agent`** |
| 3 | **`agent` 不得 import `bridge`** |

**资产 / 编排 / 消费** 三分法（天天用）：

- **资产**：磁盘 + DB 定义（`apps/`、`workflow` 表、工具定义）
- **编排**：`internal/agent`（Runner、模式、Matrix、子 Agent）
- **消费**：`bridge` + `frontend/dist`（IPC 薄层）

完整 L0–L4 登记表见 `ARCHITECTURE.md`；CI 以 `go test ./internal/arch/...` 为准。

---

## 阶段 A — Inner App 真闭环（当前施工）

| 任务 | 状态 | 说明 |
|------|------|------|
| A1 脚手架 + 侧栏新建 | ✅ | `scaffold_inner_app`、`DesktopService` |
| A2 确定性 verify + auto-fix helpers | ✅ | `verify_inner_app`、`AutoFixBundle` |
| A3 **app_builder 子 Agent**（限定 4 工具） | ✅ | `agent/app_builder.go` |
| A4 `build_inner_app_iteratively` 接 LLM 环 | ✅ | `agent/app_builder_tools.go` → `BuildInnerAppFull` |
| A5 种子 `app_builder` 进 SubagentRegistry | ✅ | `subagent_registry.go` SeedDefaults |
| A6 集成测 scaffold + verify | ✅ | `apps/scaffold_test.go` |

**不做（阶段 A）**：`api.py` 沙箱、K8s、完整 PyBot verify_app LLM 评分。

---

## 阶段 B — 工作流一条 happy path

| 任务 | 状态 |
|------|------|
| B1 模板 DAG：LLM → Tool → Notify 可保存可跑 | ✅ `wf_happy_path` + 侧栏「加载演示模板」 |
| B2 运行历史 + interrupt 恢复 UI 走通 | ✅ 历史列表 + `workflow:notify` 事件；HITL 见 `workflow_hitl_test` |
| B3 workflow 工具节点走统一 `GovernedInvoke` | ✅ `ToolsBridge` + `ComposeToolMiddleware`；fallback `GovernedInvokeJSON` |

---

## 阶段 C — 治理与心智负担

| 任务 | 状态 |
|------|------|
| C1 dynamic Python / workflow 工具 / Matrix 子 Agent 同一审批语义 | ✅ `GovernedInvoke` + `ControlPolicy` 统一纳管 |
| C2 Matrix 只维护 `matrix-orchestration.md` 一条官方路径 | ✅ 文档 + `matrix_followup` |
| C3 收窄 arch 测试为「三条硬规则」可选 | ✅ `hard_rules_test.go` (`TestHardRules`) 专项测试闭环 |

---

## 阶段 D — 对标 Cursor / Codex 原生开发体验

详见专门路线图：`cursor-codex-parity-plan.md`

| 任务 | 状态 | 说明 |
|------|------|------|
| D1 真实 PTY 交互式终端 (xterm.js) | ✅ | 支持多 Tab、动态高度拖拽、ANSI 彩色、流式输入输出与中断 |
| D2 上下文引用补全与展开 (@ Mentions & Expander) | ✅ | Composer 支持 @ 提示 + 后端自动注入 Git/终端/代码/文件上下文 |
| D3 GUI 统一多轮对话历史注入 | ✅ | `runStream` 与 `SendMessageWithSession` 统一通过 `WithHistory` 加载持久化记忆 |
| D4 上下文截断原子工具对齐归一化 | ✅ | `pruneAndNormalizeActive` 消除孤立 tool 消息，防止 LLM 400 报错 |
| D5 Monaco 内联编辑 (Ctrl+K) 快捷流转 | ✅ | 支持 `Ctrl+Enter` 接受 / `Esc` 放弃，快捷键键盘流转 |
| D6 状态栏诊断指示与 LSP 联动 | ✅ | StatusBar 增加 `⊗ / ⚠` 计数与 `WorkspaceDiagnostics` 接口 |
| D7 Monaco 行内 Diff 与审查工作区 | ✅ | `DiffViewer` 独立 Diff 对比 + `EditorView` 内联双栏 Diff 对比与应用 |
| D8 长任务中途纠偏与输入队列 (Steer Inbox) | ✅ | 任务循环中途接受用户插话纠偏并微调意图（`AddSteer` / `DrainSteers` / `SteerSession`） |
| D9 本地 Azure GPT-6 Luna 密钥自适应 | ✅ | 启动自动检测环境变量中的 Luna 凭证，设置界面提供一键切换并自动适配 reasoning_effort |

---

## 阶段 E — 后端中间件收敛与全栈“去缝合感”统一架构（2026-10）

### 核心诊断：为什么借鉴多家项目后容易产生“缝合感”？
1. **UI 术语与概念“多代同堂”**：界面文案中若直接暴露外部参考项目代号（如 `PyBot`、`Reasonix`、`OpenBitFun`、`Crush`），且对话栏用四档胶囊、设置页却用两套旧下拉框（`Profile × Canvas`），会让用户感到概念割裂。
2. **后端同职能中间件“叠床架屋”**：每引入一套新机制就往 `BuildADKHandlers` / `BuildTypedMiddlewareStack` 追加一层中间件，导致 `patchtoolcalls` 重复注册、`ToolResultPruner`（仅留 2 轮首行 80 字）与 `SessionSpine`（6 轮窗口 + 头 `5/8` 尾 `3/8`）双重裁剪打架、每轮对话重复初始化 `SessionKernel`。

### 收敛与修复清单

| 任务 | 状态 | 说明 |
|------|------|------|
| E1 **统一上下文裁剪契约（Cache-First 单一真源）** | ✅ | `SessionSpine` 默认负责 6 轮窗口 + 头 `5/8` 尾 `3/8` 确定性裁剪；`ToolResultPrunerMiddleware` 升级为头尾保留 + 报错豁免，默认关闭避免双重裁剪 |
| E2 **ADK 中间件去重与 `SessionKernel` 缓存复用** | ✅ | 移除 `handlers.go` 中重复的 `patchtoolcalls` 注册；在 `Runner` 上缓存复用 `SessionKernel` 实例，消除每轮对话重复扫描磁盘开销 |
| E3 **会话模式 (`SessionMode`) 跨重启持久化与心跳打通** | ✅ | `SetDefaultSessionMode` / `SetSessionModeForSession` 自动持久化至 `dataDir/session_modes.json`；打通 `HeartbeatManager` 至 `capability.Bus` |
| E4 **SQLite WAL 参数与内存临时表加速** | ✅ | `internal/db/sqlite.go` 保持单池原子性（保护 `:memory:` 测试与 `seq` 分配），升级 `busy_timeout=10000` 与 `temp_store=MEMORY` |
| E5 **全栈“去缝合感”：统一 AgentGo 原生术语与四档模式** | ✅ | 清理 UI 中所有第三方项目署名，将 `ChatPanel`、`AppStatusBar`、`SettingsPanel` 统一收敛为 **AgentGo 四档协作模式（极简 Minimal / 标准 Standard / 极致 Ultimate / 创造 Creative）** |

---

## 阶段 F — 收敛为「一个内核 + 多个薄壳」（2026-10 诊断后制定）

### 诊断数据（2026-10-15 实测）

| 指标 | 数值 | 说明 |
|------|------|------|
| `internal/*` 目录 | 46 个 | acp / crushproto / tui / cli / gateway / hostlink / kanban / officedoc / audio… |
| Go 生产代码 | 5.6 万行（测试 1.8 万行） | 测试占比约 32%，但没有 Agent 任务级评测 |
| `internal/bridge` | 1.2 万行 / 81 文件 / 194 个导出方法 | 前端字符串调用只用到约 85 个 |
| `AGENTGO_*` 环境开关 | 约 77 个 | 行为由隐藏开关组合决定，无法穷举测试 |
| `frontend/src/wails.ts` | 1309 行 | 超 900 行红线；字符串 `wailsCall` + `any`，无类型 |
| 入库构建产物 | `frontend/dist` 96 个文件、`eino-ext` 1031 个文件 | 每次构建 hash 改名，提交噪声极大 |

### 七个核心问题

1. **入口过多、北极星漂移**：桌面 Wails、`--serve` 网关、Zed ACP、VSCode 插件、`agentgo-cli`、Crush 协议、TUI 七个入口并行；北极星是「Inner App 闭环」，但 10 月提交主要投在 ACP / serve / trigger / vscode / cli。
2. **`bridge` 是上帝层**：业务逻辑（如 `matrix_compose`、workspace review）沉在 IPC 层，网关 / ACP / CLI 各自再包一遍，同一能力多份实现。
3. **配置靠环境变量堆**：77 个开关没有统一类型、默认值文档和设置页入口。
4. **中间件栈无顺序契约**：summarization / reduction / spill / pruner / spine 五种上下文处理并存，governance 与 tool heal 各挂两处，没有测试锁定顺序与职责。
5. **模式模型前后端不一致**：后端是 Profile × Canvas 9 种组合，前端四档模式的映射在 `ChatPanel`、`AppStatusBar`、`SettingsPanel` 各写一份；会话模式又单独存成 `session_modes.json`，没进 SQLite。
6. **前端无类型、样式按「来源项目」命名**：`wails.ts` 字符串调用；`reasonix-polish.css` 这种按出处命名的文件本身就是缝合痕迹；面板大量内联样式。
7. **仓库卫生**：构建产物与第三方源码入库；`**/*.md` 默认忽略，设计文档不进版本库，知识容易丢。

### 设计原则

- **一个内核**：定义 `internal/core`（暂名）服务接口——Sessions、Runs（以 `chat_run_events` 为唯一事件流）、Approvals、Harness、Apps、Workflows。Wails / HTTP 网关 / ACP / CLI 全部是只做协议转换的薄适配器，不含业务逻辑。
- **一个模式模型**：后端新增一等类型 `Harness`（minimal / standard / ultimate / creative），由它推导 Profile、Canvas、迭代上限、工具集；前端从后端拉取定义，不再自己维护映射。
- **一个配置对象**：`config.Runtime` 结构体统一加载（配置文件 → 环境变量覆盖），设置页提供「高级」只读视图；开关要么转正、要么删除。
- **一条上下文流水线**：上下文组装 → 预算与压缩 → 工具治理 → 遥测，四段固定顺序，在一个函数里声明，由测试锁定。

### 分期计划（每期都有可验收的结果）

| 期 | 内容 | 验收 |
|----|------|------|
| F1 收口（1 周）✅ | 冻结 TUI / VSCode 插件 / CLI；`session mode` 迁入 SQLite `session_modes` 表（旧 `session_modes.json` 启动时一次性导入后删除）；前端四档映射收为 `frontend/src/harness.ts` | 重启后模式保留（`TestRunnerPersistsSessionModesAcrossRestart`）；`ChatPanel` / `AppStatusBar` / `SettingsPanel` 均引用同一模块 |
| F2 Harness 一等化（1 周）✅ | 后端 `Harness` 类型 + `ListHarnesses`（IPC / 网关 / ACP 同源）；前端启动时拉取；`ModeHints` 暂缓（要用评测验证后再改）。详见 `docs/plan-f4-eval-f2-harness.md` §3 | 前端不再出现 profile/canvas 推断逻辑 |
| F3 中间件流水线（1 周）✅ | `ToolResultPruner` 已删（spine compile 负责裁剪）；governance / tool heal 各只挂一处（`composeToolMiddlewares` + `toolMounts`）；顺序与去重由 `middleware_pipeline_test.go` 锁定；顺手修了 canvas-aware 策略在 compose 层从未生效的问题 | `TestBuildADKHandlers_OrderAndDedup`、`TestComposeToolMiddlewares_SingleMount` |
| F4 评测基线（1–2 周）✅ 首版 | `agentgo-eval` + 8 个任务 + 每次运行的 `RunMeter`（token/缓存/工具数）+ 基线对比；详见 `docs/eval-baseline.md`、`docs/plan-f4-eval-f2-harness.md`。**发现缓存命中仅 1% → F4.1 已修（根因：脊柱/模式中间件非幂等 + 工具列表 map 乱序），缓存 1%→89%，prompt -19%**；用例已扩到 20 个（套件 v2，含多轮与审批），四档位各有基线（`docs/eval/baseline*.json`）；评测抓到并修复了「工作流中文名 → 全局 400」与「网关多轮无历史」两个真 bug | 之后改上下文策略必须附评测前后对比 |
| F5 内核抽取（2–3 周）🟡 进行中 | 已建 `internal/core`（`BuildHistory` / `Transcript`，网关与桌面共用）；工作区的搜索/Git/媒体/评审/列目录/文件操作/命令执行下沉到 `internal/workspace`（新增单测）。**`bridge` 非测试代码 12,043 → 9,146 行（含 2026-10-17 把 `RunTracker` / 待审批存储 / 审批中继下沉到 `internal/core`），离 5000 行的目标还差一半以上**，剩余多为与 `Runtime`/`AppService` 强耦合的编排（approval_resume / coding_agent / matrix_compose / ideruntime_bridge / context_expander / recovery / run_tracker），不能靠机械搬家完成，需要先定义内核服务接口。 | 5000 行目标**未达成**；按领域继续迁，每次迁移都带单测与 `go test ./internal/bridge/` |
| F6 前端类型化（1–2 周）✅ 主体 | `wails.ts`（1312 行）拆为门面 + `ipc/{runtime,call,contract,types}` + `ipc/mock/*`（浏览器预览假后端，最大文件 652 行）；`wailsCall` 按 `IpcContract` 检查方法名/参数/返回类型，未登记方法编译不过（动态方法名用显式的 `wailsCallRaw`）；`reasonix-polish.css` → `polish.css`，UI 与注释中的第三方项目名全部清除；`App.tsx` 869 行（<900）。**未做**：清内联样式、收敛 tokens | `tsc --noEmit` 0 错误；`vite build` 通过 |
| F7 配置与仓库卫生（1 周）✅ | `internal/config` 声明式开关注册表（71 项，类型化句柄，**唯一**读取 `AGENTGO_*` 的地方，`arch` 测试锁定）；设置页新增只读「高级配置」（`GetConfigSnapshot`）；`frontend/dist` 取消跟踪（`scripts/build-frontend.ps1` 生成，`.gitkeep` 满足 `go:embed`）；`eino-ext` 取消跟踪（`go.mod` 本来就不 `replace` 它，构建用模块缓存；本地联调用 `go.work`）；`docs/` 设计文档入库。**未做**：agent 与 IDE 终端共用 PTY | 普通提交不再带构建产物 diff；`TestAgentgoEnvIsOnlyNamedInConfig` |

### 已定决策

- **冻结入口（只修 bug，不加功能）**：`internal/tui`、VSCode 插件、`agentgo-cli`。
- **保留并继续投入**：桌面端（主线）、`--serve` 网关、Zed ACP、Crush 协议兼容层。
- 新能力一律先落在内核服务上，再由桌面端暴露；不再为冻结入口单独开发。

---

## 阶段 F 收尾记录（2026-10-17）

- 评测基线：四档位 100% 通过（见 `docs/eval-baseline.md`）。
- 发现并修复的真 bug：①工作流中文名导致所有请求 400；②网关多轮无历史；③governance/heal 双重挂载与 canvas 策略失效；④脊柱/模式中间件非幂等（F4.1）。
- **未完成（如实记录）**：F5 的 5000 行目标；`ModeHints`（档位名写进提示词）——评测套件只能证明"不退化"，衡量不了"模型能不能说出自己所在档位"，
  而它会固定多花几十个 prompt token，所以没有为它做对照实验，暂不加；PTY 共享；内联样式清理。

## 明确后置

- IM 渠道、Browser 工具、33 PyFlow 节点、2071 式测试规模  
- `matrix_compose` 迁出 `bridge`（等 Matrix 成日常入口）  
- 论文级 Admin 状态机、Capability 路由树  

---

## 本周提交检查清单

```powershell
cd agentgo
go test ./internal/arch/... ./internal/apps/... ./internal/agent/... -count=1
go build -o bin\agentgo.exe .\cmd\agentgo
```

手动：设置 API Key → 对话「用 build_inner_app_iteratively 创建 demo_chat」→ 应用面板打开。

### F5 下一步设计草案：审批恢复下沉（2026-10-17）

读完 ridge/approval_resume.go（334 行）后的结论：
esolveApproval 同时做了四类事，下沉前必须拆开，否则只是把耦合搬家。

| 职责 | 现在耦合在 | 目标 |
|---|---|---|
| 记录决定、抢占恢复权（Resolve / pending.Take / 
elay） | Runtime 字段 | 进 core.ApprovalFlow，只依赖 governance、PendingStore、ApprovalRelay |
| 按 ResumeKind 恢复（agent / matrix / workflow） | Runtime.resumeAgent 等方法 | core 定义 Resumer 接口，ridge 注入三种实现 |
| 运行日志收尾（waitingRunID / commitRun） | AppService + sessions | 进 core，仅依赖 sessions |
| 桌面事件与消息 DTO（pproval:resolved、chat:done、ChatMessageDTO） | s.app.Event、桌面 DTO | 留在 ridge，通过 core 返回的结果结构体翻译 |

先做第 1、3 行（无 UI 依赖），再做第 2 行；第 4 行永远留在 bridge。每步带单测并跑 go test ./internal/bridge/。

**进展（同日）：** 上表第 1、3 行已完成——`core.ApprovalFlow`（`Decide`：记录决定、通知中继、抢占恢复权）与 `core/runlog.go`（run log 事件映射、commit、`AwaitingRunID`）已下沉，带 6 个单测；`bridge/approval_resume.go` 只剩按 `ResumeKind` 恢复与桌面事件翻译。第 2 行（`Resumer` 接口）**未做**：agent / matrix / workflow 三条分支的恢复后处理差异很大（workflow 链式审批、matrix 发布编排事件、agent 走引擎 emitter），现在抽接口只会得到一个只有单一调用点的空壳；等 matrix / workflow 的 HITL 也走 `core` 时再做。

**进展（同日，续）：** 又下沉 6 块到 `internal/core`，每块都带单测：`Recovery`（重启后待审批运行的接管/关闭/过期清理）、`ExpandMentions`（@Git/@Terminal/@Codebase/@Problems/@文件；原来只有桌面聊天用，现在网关/ACP 也能复用）、`RecallQA`（记忆召回打分与解释）、`ChangeWatcher` + `ChangeSink`（编辑/shell 前后快照与 diff，Crush 适配器和 coding_agent 节点原来各抄一份，现在共用）、`MatrixOrchestrator`（App Matrix 编排图）。内核不认识具体工具插件，所以编辑类工具名由 `bridge/change_tools.go` 注入（`TestConcretePluginsAreOnlyReachedThroughTheAssembly` 守着这条边界）。

**下沉时发现的真 bug：** App Matrix 编排图的分支 `NewGraphBranch(..., map[string]bool{"execute": false, "summarize": false})`——Eino 用 `!endNodes[end]` 判断是否允许，值写成 `false` 等于全部不允许，所以只要开启 `MatrixComposeEnabled`，任何事件都会报 `branch invocation returns unintended end node`。原来没有测试，一直没被发现；已改为 `true` 并补了 4 个测试（规则计划、LLM 计划、LLM 失败回退、执行器缺失）。

**仍留在 bridge 的（有意）：** 桌面/Wails IPC 与 DTO（`*_ipc.go`、`app_ipc.go`）、`crushTurn`（Crush 协议翻译，`crushproto` 要求只用标准库，没有合适的内核归宿）、`ideruntime_bridge`（IDE 文件/终端/Git 的 IPC 包装）。下一批可以抽的是 `workflow_rc`（工作流工具运行器，280 行，依赖 `Runtime` 的治理/工具注册表）。
