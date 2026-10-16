# 评测基线（agentgo-eval）

用一套固定任务跑真实的 Agent，量化"答对率 / token / 缓存命中 / 工具调用数"，并与已提交的基线对比。
任何会影响提示词、上下文裁剪、中间件顺序、工具集的改动，合并前都应该跑一次。

## 运行

```powershell
# 1. 起无头服务。必须把工作区指到 agentgo 仓库——事实类用例要读仓库里的文件
$env:AGENTGO_WORKSPACE_ROOT = 'C:\Users\wgk\Documents\GitHub\agentgo'
agentgo --serve

# 2. 另一个终端：跑评测并与基线对比（退出码 0 正常，1 回归，2 用法/运行错误）
agentgo-eval --runs 3 --out docs/eval/current.json --baseline docs/eval/baseline.json
```

- 地址与令牌默认读 `agentgo-serve.json`（数据目录下），也可用 `--addr` / `--token` 指定。
- 用例**串行**执行，每次运行使用带时间戳的会话 id，不会复用上一次评测的对话历史。
- `--suite my.json` 可换任务集；格式见 `internal/evaluation/suite_default.json`。
- `--harness minimal|standard|ultimate|creative` 让所有会话按指定档位运行（档位列表见 `GET /api/v1/harnesses`）。不同档位的工具集和提示不同（实测 `minimal` 约 3450 prompt token，`standard` 约 3900），**基线只对同一档位有意义**，换档位请另存基线。

> 第一次踩的坑：没设 `AGENTGO_WORKSPACE_ROOT` 时，工作区不是仓库（与启动目录无关），6 个事实题全部答"找不到文件"。
> 这是环境前置条件，不是 Agent 退化。

## 指标含义

| 指标 | 来源 | 说明 |
|------|------|------|
| `prompt` / `completion` / `reasoning` | 每次模型调用的 `TokenUsage` 累加 | 一次运行里 Agent 循环会多次调用模型，这里是总和 |
| `cached` | `PromptTokenDetails.CachedTokens` | 服务商报告的前缀缓存命中 token |
| `tools` | 工具调用中间件计数 | 含失败的调用 |
| 通过 | 正则 / 关键词评分，可附 `max_tool_calls` | `plain-*` 用例要求 0 次工具调用，用来发现"不该用工具却用了" |

数据通路：模型回调 → `RunMeter`（每次运行一个）→ 网关 `done` 事件的 `usage` 字段 → `HTTPAgent` → `EvalResult`。
同一条回调也会写入 `ledger` 的按日汇总（之前 `ModelUsageTracer` 没有任何写入点，现在每次模型调用都会记录）。

## 回归判定（默认阈值）

| 项 | 阈值 | 结果 |
|----|------|------|
| 通过率下降 | > 10 个百分点 | 失败（退出码 1） |
| 每次运行平均 prompt token 增长 | > +20% | 失败 |
| 缓存命中率下降 | > 10 个百分点 | 仅警告 |
| 基线通过、现在不通过的用例 | 任意 | 点名列出（不单独导致失败） |

**噪声**：同一套件连续两次运行，prompt token 总量相差约 10–15%（模型会随机多读一两个文件）。
所以基线应使用 `--runs 3`，对比时也建议 `--runs 3`；阈值可用 `--max-pass-drop` / `--max-token-growth` / `--max-cache-drop` 调整。

## 更新基线

只在"有意改变行为并确认更好"之后更新：

```powershell
agentgo-eval --runs 3 --out docs/eval/baseline.json
```

提交时在提交信息里写清楚为什么变（例如"裁剪参数调整，prompt token -25%"）。

## 当前基线（2026-10-17，套件 v2，模型 gpt-6-luna，每档 `--runs 3`）

套件 v2 共 20 个用例（事实题、纯问答、报错堆栈定位、多轮同会话、审批暂停、Inner App）。
用例可用 `skip_harnesses` 声明对某些档位不适用：`minimal` 是只读调查档（不允许写文件/执行命令），
审批类用例对它没有意义；`approval-bash` 还要求 `admin` / `app_matrix` 档位，因为 `assistant × balanced` 按
`mode_policy.AllowsDynamicTool` 本来就不暴露 `execute_bash`。不带 `--harness` 时，评测器向网关查询默认档位再决定跳过哪些用例。

| 基线文件 | 档位 | 用例 | 通过 | 运行 | prompt token | 缓存命中 | 工具调用 |
|---|---|---|---|---|---|---|---|
| `docs/eval/baseline.json` | standard（服务默认） | 19 | 19/19 | 57 | 786,759 | **94%** | 103 |
| `docs/eval/baseline-minimal.json` | minimal | 17 | 17/17 | 51 | 651,851 | 85% | 89 |
| `docs/eval/baseline-ultimate.json` | ultimate | 20 | 20/20 | 60 | 857,781 | 87% | 98 |
| `docs/eval/baseline-creative.json` | creative | 20 | 20/20 | 60 | 872,847 | 88% | 98 |

> 基线只对同一档位有意义。对比时：`agentgo-eval --runs 3 --harness minimal --baseline docs/eval/baseline-minimal.json`。

### 这轮评测抓到的真问题（不是评测噪声）

1. **工作流名称含中文/标点时，`workflow_<名称>` 不是合法的函数名（`^[a-zA-Z0-9_.-]+$`），上游直接对整个请求返回 400**——
   也就是只要库里有一个叫「预览 Happy Path」的工作流，**所有对话都发不出去**。已修：`workflow.SafeToolName`
   统一净化（纯 ASCII 名保持旧映射，其余加哈希后缀防冲突），`Registry.AddTool` 兜底拒绝非法名并记录日志。
2. **多轮对话在网关里没有历史**（模型第二轮不记得第一轮）。已修：`core.Transcript` 统一加载/落库历史。
3. 后加的 6 个事实/定位用例带 `min_tool_calls`，防止模型凭记忆答题（最早的 6 个事实题没有这个约束）。

### 评测器自身的两处加固

- **上游 5xx / 429 自动重试**（最多 2 次，换新会话重试，避免第一次半途落库污染历史）。之前 60 次运行里稳定会有 1–3 次
  `500 The service hit an internal error`，让通过率在 85%–95% 之间抖动，掩盖了真问题。400 这类不重试。
- 默认档位解析 + `skip_harnesses`，避免"用例对该档位本不可能通过"被当成回归。

### 与旧基线的关系

旧基线（8 用例、89% 缓存）是另一套件，用例集不同，**不可直接比较**；新基线从套件 v2 重新起算。

### 历史：F4.1 之前的基线（2026-10-15）

| | 修复前 | 修复后 |
|---|---|---|
| 缓存命中 | 4,474 token（**1%**） | 236,430 token（**89%**） |
| prompt token（同样 24 次运行） | 327,093 | 265,717（-19%） |
| 系统消息 | 61 次请求里 37 种，3,186 → 11,956 字符逐次变长 | 1 种，固定 2,303 字符 |
| 相邻请求的第一个差异位置 | 第 88 字节 | 第 3,557 字符（已进入对话部分） |

### 缓存为什么是 1%：四个叠加的问题（F4.1，已修）

1. **`ModeProfileMiddleware` 每次模型调用都往系统消息追加一遍模式提示**（没判重）。
2. **脊柱（`SessionSpine`）把渲染结果写回状态，下一轮又当原始输入再包一层 `<system_rules>`**，并把所有块折回规则里。
3. **脊柱把 eino 工具搜索插入的"延迟工具提醒"合并进了自己的系统消息，丢掉了框架用来判重的 `Extra` 标记**，框架下一轮认为没插过又插一次。
4. 另外 `Registry.GetAllTools/GetStaticTools/GetDynamicTools` 遍历 Go map，**工具数组和提醒里的工具名顺序每次随机**（61 次请求出现 20 种顺序）。

结果：系统提示每次调用都变，且从第 88 字节就变，前缀缓存不可能命中；同时系统提示每次多 ~2k 字符，一次多步任务的 token 被白白放大。
修复见 `docs/plan-f4-eval-f2-harness.md` 第 2b 节；每一项都有回归测试。

### 如何自己复查前缀稳定性

```powershell
$env:AGENTGO_DUMP_LLM_DIR = 'D:\tmp\agentgo-dump'   # 把每次发往模型的请求体写成文件（含完整提示词，勿提交、勿放公共目录）
agentgo --serve
# 跑一遍评测，然后逐字节比较相邻请求的公共前缀；任何一次"系统消息变了"都是缓存杀手
```

> 噪声提醒：一次未诊断的运行里，8 个用例有 3 个失败（修复前、同一套件、同一模型），之后 6 次运行全部通过。
> 通过率有偶发抖动，所以回归判定用 10 个百分点的阈值而不是"必须全过"。

## 后续可以补的用例

- 套件 v2 已达 20 个。仍缺：真正"写文件 → 跑测试 → 读结果"的多步闭环（需要沙箱工作区，避免污染仓库）；
  审批后"批准/拒绝"两条分支的续跑（目前只验证到"暂停等待审批"）。
