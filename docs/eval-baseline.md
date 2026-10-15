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

## 当前基线（2026-10-15，模型 gpt-6-luna，3 次 × 8 用例）

- 通过 8/8，24 次运行，共 123 秒。
- prompt 327,093 / completion 1,582 / 工具调用 42 / 模型调用 66。
- **缓存命中 4,474 token，约 1%。** 这是本基线最重要的发现，见下。

### 发现：前缀缓存基本没命中

`plain-arith` 这类用例每次都发送同一份约 3.9k token 的系统提示，按理第二次起应命中缓存，但 24 次运行里只有 1 次拿到缓存。
读取链路没问题（出现过非零值），说明服务商会返回缓存字段，**问题在请求前缀不稳定，或中转不做前缀缓存**。
需要的下一步（记入 `docs/plan-f4-eval-f2-harness.md` 的 F4.1）：

1. 对同一会话连续两次请求，逐字节 diff 发往模型的消息序列，找出最早出现差异的位置（嫌疑：系统提示里的时间/日期、记忆注入、工具列表顺序）。
2. 用直连官方接口与当前中转各跑一遍，区分"我们的前缀不稳"与"中转不缓存"。
3. 修复后重跑评测，缓存命中应明显上升，且通过率不降。

## 后续要补的用例

- 多步修改 + 验证（写文件、跑测试）。
- 报错堆栈定位：专门验证"裁剪不砍报错尾部"。
- 需要审批的危险操作（目前遇到审批会判为失败）。
- Inner App 调用。
- 目标：20 个用例。
