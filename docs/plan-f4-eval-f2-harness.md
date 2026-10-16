# 计划：F4 评测基线 → F2 Harness 一等化

> 上位文档：`docs/plan.md` 阶段 F。本文是阶段 F 里最近两期的施工细则，附前文约束与经验记录。
> 更新日期：2026-10-15

## 0. 前文约束（做事前先对照）

**已定决策**
- 冻结（只修 bug，不加功能）：`internal/tui`、VSCode 插件、`agentgo-cli`。
- 保留并继续投入：桌面端（主线）、`--serve` 网关、Zed ACP、Crush 协议兼容层。
- 已完成：F1（会话模式进 SQLite，前端四档映射收到 `frontend/src/harness.ts`）。

**工程约束**
- 小/中项目单文件不超过 900 行；超了按功能拆。当前 `frontend/src/wails.ts`（1309 行）、`internal/agent/runner.go`（接近上限）要留意，新代码不往里堆。
- `internal` 分层硬规则：除 `bridge` 外不得 import `bridge`；资产层（`apps`/`workflow`/`tools`）不得 import `agent`；`agent` 不得 import `bridge`。改完跑 `go test ./internal/arch/...`。
- 不使用子 Agent、不设定时任务；长任务用后台命令 + 等待，不在对话里轮询。
- 缓存与构建目录不放 C 盘（C 盘紧张）；临时脚本和参考仓放 `D:\reference_repos`。
- 终端统一用 pwsh7；复杂文本处理写 Python 脚本；`git commit -m` 的信息里不要带会被 PowerShell 误解析的引号结构。
- 回复一律简体中文；改完要写 MD 记录问题与下一步（本文第 5 节）。

## 1. 为什么先做 F4（评测基线）

上一轮把工具输出裁剪参数改成了"保留 6 轮、头 5/8 尾 3/8、报错豁免"，F1 又动了模式持久化。这些改动目前只有单元测试，**没有任何数据证明 Agent 的实际表现变好或变差**。
没有基线，F2/F3/F5 这类重构只能凭感觉，所以先建基线。

仓库里已有 `internal/evaluation`（`EvalSet` / `EvaluateSet` / `ScoreCase`，并发运行 + 正则/关键词评分），以及 `internal/ledger` 的用量记录（含 `CachedTokens`）。缺的是：

| 缺口 | 说明 |
|------|------|
| 真实任务集 | 目前只有测试里的玩具用例 |
| 指标 | `CaseResult` 只有分数与耗时，没有 token、缓存命中、工具调用次数 |
| 接 `--serve` 的 Generator | 现有 `AgentGenerator` 只有接口，没有 HTTP 实现 |
| 基线对比 | 没有"保存结果 → 下次运行与基线比较 → 回归则失败" |

## 2. F4 设计

### 2.1 数据流（✅ 已实现；与初稿相比改了一处）

```
agentgo-eval ──HTTP SSE──▶ agentgo --serve (/api/v1/chat/stream)
      │                         │ 模型回调 / 工具中间件 → RunMeter（每次运行一个）
      │◀──── done 事件的 usage ──┘ 同一回调也写 ledger 日汇总
      ▼
  result.json  ── 与 baseline.json 比较 ──▶ 退出码 0/1/2
```

> **设计变更**：初稿是"读 `/api/v1/usage` 日汇总，运行前后取差"。实现时发现 `ModelUsageTracer.Record`
> 在生产代码里**从来没人调用**（tracer 建好了却是空的），而且日汇总差值在并发下不准、还依赖 flush。
> 改为每次运行一个 `RunMeter`，精确、与并发无关，不需要新网关端点；同时让 Runner 始终把用量写进 ledger，激活这段死代码。

- 评测用例仍然**串行**运行（时间稳定、一次只占一个后端会话）。
- 评分沿用 `ScoreCase`（正则 / 关键词），不引入 LLM 评审（避免评测本身不稳定）。新增 `max_tool_calls`。

### 2.2 改动清单（✅ 全部完成）

| 文件 | 内容 |
|------|------|
| `internal/agent/run_meter.go`（新） | `RunUsage` / `RunMeter` / `WithRunMeter`；`Runner.runUsageHandler` 把每次模型调用写入 ledger 和 RunMeter |
| `internal/agent/usage_observer.go` | 回调改为带完整 `TokenUsage`（含缓存、推理）的 sink；`newUsageCallbackHandler` 签名不变 |
| `internal/agent/adk_run.go` / `tool_event_mw.go` | `queryOptions` 始终挂用量回调；工具调用计数 |
| `internal/gateway/types.go`、`internal/bridge/gateway_backend.go` | `ChatResult.Usage`，`StreamChat` 绑定 `RunMeter` 并在 `done` 事件带回 |
| `internal/evaluation/metrics.go`（新） | `Metrics`、`MeasuredAgent`、`Compare` / `Thresholds` / `HasFailure`、`SaveResult` / `LoadResult` / `DefaultSuite` |
| `internal/evaluation/eval.go` | `CaseResult.Metrics`、`EvalResult.Totals/TotalRuns`、`EvaluateSetTagged`（会话 id 带运行标签）、`max_tool_calls` |
| `internal/evaluation/httpagent.go`（新） | 连接 `--serve` 的 `MeasuredAgent`，解析 SSE |
| `internal/evaluation/suite_default.json`（新，`go:embed`） | 默认任务集（8 个） |
| `cmd/agentgo-eval/main.go`（新） | 命令行入口 |
| `docs/eval-baseline.md`、`docs/eval/baseline.json`（新） | 使用说明与首份基线 |

### 2.3 默认任务集（第一版 8 个，目标 20 个）

任务以 agentgo 仓库自身为工作区，答案取自稳定的代码事实，评分用正则：

| ID | 类型 | 问题要点 | 期望 |
|----|------|----------|------|
| fact-busy-timeout | 单文件检索 | `internal/db/sqlite.go` 的 `busy_timeout` 值 | `10000` |
| fact-default-mode-key | 单文件检索 | `internal/sessions/modes.go` 默认模式 key | `__default__` |
| fact-keep-recent | 单文件检索 | 工具输出裁剪默认保留几轮 | `6` |
| fact-harness-ids | 单文件检索 | `frontend/src/harness.ts` 四档 id | 含 minimal/standard/ultimate/creative |
| fact-cancel-route | 跨文件检索 | 网关取消运行的路由 | `/api/v1/chat/cancel` |
| fact-error-exempt | 理解 | `isLikelyToolErrorOutput` 豁免哪些输出 | 含 `panic` 与 `exit status` |
| plain-arith | 不用工具 | 17×23 | `391`（并要求不调用工具，作回归哨兵） |
| plain-no-tool-chat | 不用工具 | 一句话介绍自己 | 含 `AgentGo` |

要补的类型（第二版）：多步修改 + 验证、报错堆栈定位（专门验证"裁剪不砍报错尾部"）、需审批的危险操作、Inner App 调用。

### 2.4 回归判定（默认阈值，可用参数覆盖）

- 通过率下降超过 1 个用例（或 > 10%）→ 失败。
- 平均 prompt token 增长超过 20% → 失败。
- 缓存命中率下降超过 10 个百分点 → 警告（模型端波动大，不作硬失败）。

### 2.5 验收（✅ 2026-10-15）

1. ✅ `go test ./internal/evaluation/ ./internal/agent/ ./internal/arch/...` 通过；新增单测覆盖 RunMeter（流式/非流式/缓存/ledger/工具计数）、SSE 解析（成功/鉴权失败/error/interrupt/截断）、`Compare` 各类回归、任务集格式。
2. ✅ 真机端到端：构建 `agentgo` 以 `--serve` 启动，`agentgo-eval` 跑完 8 个用例，输出 `result.json`。
3. ✅ `--baseline` 与同套件上一次结果对比，退出码 0（"no regression"）。
4. ✅ 首份基线 `docs/eval/baseline.json`（3 次 × 8 用例，gpt-6-luna）已生成并入库。

### 2.6 F4 的发现

| 发现 | 证据 | 去向 |
|------|------|------|
| **前缀缓存几乎没命中**（1%） | 66 次模型调用只有 1 次返回缓存（4474 token）；`plain-arith` 每次发同样 ~3.9k 系统提示也没命中 | ✅ F4.1 已修，见 2b（现 89%） |
| 一个只需 0 次工具的问答，prompt 也要 ~3.9k token | `plain-arith` prompt=3904 | 记录；F3 精简中间件/提示词时用它作对照 |
| 简单事实题平均 2–3 次工具调用、1.4–2.6 万 prompt token | 基线 | 作为 F3 的对照指标 |
| 同套件两次运行 prompt token 噪声 10–15% | run2 vs run3 | 基线用 `--runs 3`；阈值 +20% 偏紧但可用 |
| 评测依赖 `AGENTGO_WORKSPACE_ROOT` | 首跑 6 题全挂 | 写入使用文档的前置条件 |

## 2b. F4.1 缓存命中调查（✅ 2026-10-16，缓存 1% → 89%）

目标：弄清 1% 的命中率是"我们的前缀不稳"还是"中转不缓存"，如果是前者就修。
**结论：是我们的前缀不稳，中转没问题。** 抓请求（`AGENTGO_DUMP_LLM_DIR`）后一次就看出来了：

| 现象 | 证据 | 根因 | 修复 | 回归测试 |
|------|------|------|------|----------|
| 相邻请求从第 88 字节就不同 | 同会话 3 次调用的公共前缀 88/114/140 字节 | 脊柱把已渲染的系统消息当原始输入，每次再包一层 `<system_rules>` | `ExtraProjectedBase` 标记：再次编译时用渲染前的原始输入（`sessions/projection.go`、`compile.go`） | `TestCompileProjectedViewIsIdempotentAcrossModelCalls` 等 3 个 |
| 系统消息每次多 ~2.2k 字符 | 长度 3186→5366→7546… | `ModeProfileMiddleware` 每次调用都追加模式提示 | 已含则不追加（两条路径） | `TestModeProfileMiddlewareIsIdempotent` |
| 工具提醒每次多 883 字符 | 提醒文字重复出现 | 脊柱把 eino 工具搜索插入的提醒消息合并掉，丢了框架判重用的 `Extra` 标记，框架下一轮又插一次 | 提醒消息绕过投影，原位放回（`agent/session_spine_reminders.go`） | `TestSpineKeepsFrameworkReminderMarkerSoItIsInsertedOnce` 等 2 个 |
| 工具数组顺序每次不同 | 61 次请求出现 20 种顺序 | `Registry.GetAllTools/GetStaticTools/GetDynamicTools` 遍历 Go map | 按名字排序（`sortedToolsLocked`） | `TestRegistryToolListOrderIsDeterministic` |

修复前后（同一套 8 题 × 3 轮，均 8/8 通过）：

| | 修复前 | 修复后 |
|---|---|---|
| 缓存命中 | 1% | **89%** |
| prompt token | 327,093 | 265,717（-19%） |
| 系统消息种类 / 长度 | 37 种 / 3,186→11,956 字符 | 1 种 / 固定 2,303 字符 |

`docs/eval/baseline.json` 已更新为修复后的结果。

> 这是评测基线的第一笔回报：Cache-First 是 agentgo 的核心卖点，之前实际上完全没有兑现——而且 `plan.md` 里"脊柱是裁剪的唯一来源"这条设计，本身就是造成问题的地方。

**仍待观察的点**
- `paradigm.go:331` 也有"在消息最前面插入系统消息"的写法，当前默认配置未触发；若启用范式引擎需要同样的幂等检查。
- 评测里偶发过一次 3/8 失败，未能复现（见 `docs/eval-baseline.md`）。
- 工具数组仍有 2 种顺序（21 个 / 22 个工具），是工具搜索加载了新工具后的正常变化，不是随机。

## 3. F2 Harness 一等化（✅ 2026-10-16）

目标：后端有 `Harness` 一等类型，所有客户端入口读同一份定义。

| 步骤 | 状态 | 内容 |
|------|------|------|
| 3.1 | ✅ | `internal/agent/harness.go`：`Harness{ID,Name,Label,Desc,Profile,Canvas}`、`Harnesses()`、`HarnessByID`、`ResolveHarness`（含旧 ACP id 别名）、`SessionMode.Harness()`（非预设组合取最近预设） |
| 3.2 | ⏸ 暂缓 | `ModeHints` 不改。见下方"决定" |
| 3.3 | ✅ | IPC：`ListHarnesses`、`SetSessionHarness`，模式响应带 `harness` 字段；网关：`GET /api/v1/harnesses`、聊天请求可带 `harness`；ACP：模式列表直接取自 `Harnesses()`，`architect/coder/ask` 作为别名继续可用 |
| 3.4 | ✅ | 前端 `harness.ts` 启动时 `loadHarnesses()` 从后端拉取；内置默认值仅作离线/浏览器预览兜底 |
| 3.5 | ✅ | 测试：预设与 SessionMode 往返、非预设组合、别名、返回副本；前端兜底与后端逐字段一致（解析 `harness.ts`）；三个入口（IPC/ACP/网关）列表一致；网关端点（含鉴权/方法/501）；`--harness` 请求体 |

**验收**
- ✅ 新增或改名一个档位只改 `internal/agent/harness.go` 一处（外加前端兜底，由 Go 测试盯住不漂移）。
- ✅ 前端不再有 profile/canvas → 档位的推断规则：`harnessOf` 只做与后端预设的精确匹配，对不上就显示 `profile · canvas`。
- ✅ 真机：`GET /api/v1/harnesses` 返回四档；`harness=minimal` 的会话生效；未知档位明确报错。

**决定与取舍**
- **`ModeHints` 暂缓**：初稿想让提示词里出现用户看到的档位名。这会改动发给模型的系统提示，属于行为变更，应当用评测验证收益后再做，而不是顺手改。已有 `agentgo-eval --harness` 可以按档位跑，下一步可以做"加 harness 名行 vs 不加"的对照。
- **ACP 默认档位从 `coder` 变为 `standard`**，旧 id 通过别名保留（`architect→ultimate`、`coder→standard`、`ask→minimal`）。注意 `architect` 原来是 `app_matrix+deep`，现在落到 `admin+deep`，行为更接近"深度模式"但不完全相同，Zed 用户首次切换时可能感觉到差异。
- **前端 `dist` 未重建**：`frontend/dist` 是入库的构建产物（F7 要处理），本次只做了 `tsc --noEmit`。桌面端要看到前端改动需要重新 `vite build`。

**顺带发现**：`harness=minimal` 的 prompt 约 3450 token，`standard` 约 3900，档位确实会改变工具集/提示，评测应当按档位各跑一份基线。

## 4. 执行顺序与提交粒度

1. 本文入库（含 `.gitignore` 放行）。
2. F4：`metrics.go` + 单测 → `httpagent.go` + 单测 → 网关 `/api/v1/usage` → `cmd/agentgo-eval` → 任务集与文档。每步跑对应包测试。
3. 提交 F4（一个提交），推送。
4. F2：后端类型 + 桥接/网关 → 前端改为拉取。提交 F2，推送。
5. 每期收尾更新本文第 5 节与 `docs/plan.md` 状态。

## 5. 经验与问题记录（持续追加）

| 日期 | 问题 | 教训 |
|------|------|------|
| 2026-10-15 | 把 SQLite `MaxOpenConns` 从 1 放大到 4，`:memory:` 测试直接失败（每个连接各自一份内存库，出现 `no such table`） | 单连接池同时保护三件事：`:memory:` 测试、连接级 pragma（`foreign_keys` / `busy_timeout`）、`MAX(seq)+1` 式序号分配的原子性。要放开并发必须先把这三件事改掉，不能只改数字 |
| 2026-10-15 | 以为 `ToolResultPruner` 与 `SessionSpine` 的裁剪是重复的就默认关闭 | 没有评测数据，"默认关闭"本身也是一次未经验证的行为改动——这正是先做 F4 的原因 |
| 2026-10-15 | 把设计文档写进 `docs/` 却被 `.gitignore` 的 `**/*.md` 默认忽略，前几份文档一直没入库 | 新增文档要同时检查 `.gitignore` 白名单 |
| 2026-10-15 | `git commit -m` 在 PowerShell 里夹带引号/trailer 结构时出现解析错误 | 提交信息用简单 ASCII，不含引号嵌套；复杂的走脚本文件 |
| 2026-10-15 | 前端四档映射在三个文件各写一份，文案还不一致 | 同一概念只能有一个定义点；以后凡是"枚举 + 映射"都先建单独模块 |
| 2026-10-15 | `ModelUsageTracer` 写得很完整（带原子落盘、按日汇总），但 `Record` 在生产代码里零调用 | 写了不接线的模块 = 死代码。新增"采集型"模块时，同一个提交里必须有真实调用点和一个断言"数据确实被写入"的测试 |
| 2026-10-15 | 评测首跑 6 个事实题全挂，我先以为 Agent 退化了；实际是 `--serve` 的工作区不是仓库 | 评测失败先查环境前置条件，再怀疑被测对象；前置条件要写进使用文档和用例说明 |
| 2026-10-15 | 评测里 `taskkill` 式强杀 `--serve`，留下过期的 `agentgo-serve.json` | 测试实例用完要清理它写的状态文件；杀进程按完整路径匹配，不碰用户正在用的桌面端 |
| 2026-10-15 | 同一套件两次运行 prompt token 差 10–15% | 单次对比不可信；基线和对比都用 `--runs 3`，阈值要大于噪声 |
| 2026-10-16 | 缓存命中 1%：我先怀疑中转不缓存，实际是我们自己每次改写系统消息 | **先抓真实请求体再下结论。** 一个只读的转储开关（`AGENTGO_DUMP_LLM_DIR`）+ 20 行 Python 逐字节比对，几分钟就定位了；"猜是服务商问题"会白白浪费调查时间 |
| 2026-10-16 | "合并/重写消息列表"的中间件会静默破坏其他中间件的状态标记（脊柱丢了 eino 的 `Extra` 判重标记） | 凡是整体重建消息列表的逻辑，必须透传框架打了标记的消息；写中间件时每个 `BeforeModelRewriteState` 都要问："连续调用 N 次，输出是否一样？"并写成测试 |
| 2026-10-16 | 对 `map` 做 `range` 生成要发给模型的内容 | 任何进入提示词/工具列表的集合都必须排序；Go map 顺序随机，会让前缀每次不同 |
| 2026-10-16 | 一个"每次调用都会追加一次"的 bug 在单元测试里完全看不见（每个单测只调一次） | 对"会被反复调用"的转换写多轮幂等测试：同一输入连续喂回自己的输出 5 次，断言不变 |
| 2026-10-16 | F2 前端想"去掉推断规则"时，发现另一套（ACP 的 architect/coder/ask）也在后端各写一份 | 清理"同一概念多处定义"时先全仓搜一遍同义入口（IPC、ACP、网关、环境变量），不要只看前端；本次三个入口现在由一个测试保证一致 |
| 2026-10-16 | 前端兜底列表是后端的副本，注释里写"有测试盯住"很容易变成空话 | 凡是写了"必须与 X 保持一致"，同一提交里就写一个会失败的测试（这里是解析 `harness.ts` 对比 Go 定义） |
| 2026-10-16 | `gofmt -w` 可能连带改行尾 | 对 CRLF 工作区的文件，格式化后先看 `git diff --stat`，确认只有预期行变化再提交 |
| 2026-10-16 | Grep 工具对指定到模块缓存目录（工作区外）的搜索结果混入了工作区文件 | 搜工作区外的目录用 `rg` 直接在 shell 里查，别依赖 Grep 工具的 path |

### 下一步（滚动）

1. ✅ F4 评测基线（已完成并有首份基线）。
2. ✅ F4.1 缓存命中调查（1% → 89%，prompt -19%）。
3. ✅ F2 Harness 一等化。
3a. **按档位各跑一份基线**（`agentgo-eval --harness minimal|ultimate|creative`），并用它验证 `ModeHints` 是否值得加档位名。
3b. 补评测用例到 20 个（多步修改+验证、报错堆栈定位、审批、Inner App），并加一个"多轮同会话"用例专门盯前缀稳定性。
4. F3 中间件流水线：用评测前后对比证明收益（prompt token、工具调用数、缓存命中）。
5. 之后按 `docs/plan.md` 的 F5 → F6 → F7 推进。
