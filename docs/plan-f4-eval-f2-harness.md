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
| **前缀缓存几乎没命中**（1%） | 66 次模型调用只有 1 次返回缓存（4474 token）；`plain-arith` 每次发同样 ~3.9k 系统提示也没命中 | **F4.1，下一项** |
| 一个只需 0 次工具的问答，prompt 也要 ~3.9k token | `plain-arith` prompt=3904 | 记录；F3 精简中间件/提示词时用它作对照 |
| 简单事实题平均 2–3 次工具调用、1.4–2.6 万 prompt token | 基线 | 作为 F3 的对照指标 |
| 同套件两次运行 prompt token 噪声 10–15% | run2 vs run3 | 基线用 `--runs 3`；阈值 +20% 偏紧但可用 |
| 评测依赖 `AGENTGO_WORKSPACE_ROOT` | 首跑 6 题全挂 | 写入使用文档的前置条件 |

## 2b. F4.1 缓存命中调查（新增，F2 之前做）

目标：弄清 1% 的命中率是"我们的前缀不稳"还是"中转不缓存"，如果是前者就修。

1. 抓两次相邻请求发往模型的完整消息序列，逐字节 diff，定位最早的差异位置（嫌疑：系统提示里的时间/日期、记忆注入、工具列表顺序、`SessionSpine` 的裁剪标记）。
2. 同一份请求分别发给官方接口和当前中转，比较 `cached_tokens`。
3. 修复后重跑 `agentgo-eval --baseline docs/eval/baseline.json`：缓存命中应明显上升、通过率不降、prompt token 不涨。
4. 若确认是中转不缓存：在文档里记录，并把"缓存命中"从硬指标改为仅在直连时有意义的参考指标。

> 这一步的价值最大：Cache-First 是 agentgo 的核心卖点之一，而基线显示它目前没有兑现。

## 3. F2 设计（F4 完成后做）

目标：后端有 `Harness` 一等类型，前端不再维护映射。

| 步骤 | 内容 |
|------|------|
| 3.1 | `internal/agent/harness.go`：`Harness{ID, Name, Desc, Profile, Canvas}`；`Harnesses()` 返回四档；`HarnessOf(SessionMode)` 反推 |
| 3.2 | `ModeHints` 增加 harness 标识，让提示词里出现的是用户看到的四档名而不是 profile×canvas |
| 3.3 | 桥接新增 `ListHarnesses`；网关新增 `GET /api/v1/harnesses`（ACP 的 `SetSessionMode` 也接受 harness id） |
| 3.4 | 前端 `harness.ts` 改为启动时从后端拉取，保留内置默认值作离线兜底 |
| 3.5 | 单测：四档与 profile/canvas 的双向映射；旧会话里存的 profile/canvas 组合能反推到合理档位 |

验收：新增或改名一个档位只改后端一处；前端不出现 profile/canvas 字面量（`harness.ts` 兜底除外）。

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

### 下一步（滚动）

1. ✅ F4 评测基线（已完成并有首份基线）。
2. **F4.1 缓存命中调查**（见 2b）。
3. F2 Harness 一等化。
4. F3 中间件流水线：用评测前后对比证明收益（prompt token、工具调用数、缓存命中）。
5. 之后按 `docs/plan.md` 的 F5 → F6 → F7 推进。
