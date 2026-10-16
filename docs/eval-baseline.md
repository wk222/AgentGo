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

## 当前基线（2026-10-16，模型 gpt-6-luna，3 次 × 8 用例）

- 通过 8/8，24 次运行，共 148 秒。
- prompt 265,717 / completion 1,479 / 工具调用 37 / 模型调用 61。
- **缓存命中 236,430 token，89%。**

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

## 后续要补的用例

- 多步修改 + 验证（写文件、跑测试）。
- 报错堆栈定位：专门验证"裁剪不砍报错尾部"。
- 需要审批的危险操作（目前遇到审批会判为失败）。
- Inner App 调用。
- 目标：20 个用例。
