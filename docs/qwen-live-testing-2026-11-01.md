# qwen3.7-max 实战压测记录（2026-11-01）

用真实端点 `https://market.100812.xyz/v1` + `qwen/qwen3.7-max` 跑 AgentGo 的真实任务，
不是评测套件。下面是跑通过程、发现的 bug、修复与验证。

## 跑之前必须先做的一件事

**环境里只要有 `AZURE_OPENAI_API_KEY` + `AZURE_OPENAI_API_ENDPOINT`，`agentgo-cli` 会静默改用
Azure `gpt-6-luna`**（`cmd/agentgo-cli/backend.go`），config.json 里存的 qwen 完全不生效。
我第一次的整轮 CLI 实测全打在 Azure 上，直到用 `AGENTGO_DUMP_LLM_DIR` 查看请求体的
`model` 字段才发现。

跑 qwen 前必须把 Azure 变量从进程环境里去掉，否则测的不是 qwen：

```powershell
foreach ($n in 'AZURE_OPENAI_API_KEY','AZURE_OPENAI_ENDPOINT','AZURE_OPENAI_API_ENDPOINT','AZURE_OPENAI_API_VERSION') {
    [Environment]::SetEnvironmentVariable($n, $null, 'Process')
}
$env:AGENTGO_DUMP_LLM_DIR = '<dump-dir>'   # 用它核对 model 字段
```

`-w` 指定的工作区与进程 cwd 无关，所以测试都在 `cwd=C:\` 下跑，正好能暴露路径基准的 bug。

## 结论

能跑，且实际能力不错；但暴露了 4 个真 bug，其中 2 个会直接导致任务失败或安全越界。

| # | 问题 | 影响 | 状态 |
|---|---|---|---|
| 1 | `code_grep` / `code_glob` 把相对 `path` 按**进程 cwd** 解析，`code_ls` 却按工作区 | cwd 是 `C:\` 时 grep 全盘找不到文件、glob 扫遍整个盘 | 已修 |
| 2 | `code_grep` / `code_glob` 对工作区外路径**不经过审批**就直接读 | `AGENTGO_CODETOOLS_APPROVE=0` 也拦不住，可读任意文件 | 已修 |
| 3 | grep 截断时首行写 `Found 100 matches`，像总数 | 模型把分页大小当答案（问 2141 行答 100） | 已修 |
| 4 | 多进程共享 agentgo.db 时 `AppendContextEvents` 报 `database is locked` | 降级成无工具闲聊，模型谎称"不支持创建文件"，任务静默失败 | 已修 |
| 5 | `AGENTGO_API_KEY` 文档声明"覆盖 config.json"，实际优先级更低 | 启动器/CI 想临时换 key 换不掉 | 已修 |
| 6 | `SymbolSearch` 绕过审批：`lsp_definition`/`lsp_references`/`lsp_call_hierarchy` 可按符号名读工作区外文件 | 与 bug 2 同类，但走 LSP 这条路，模型拿到的是匹配行内容 | 已修 |

## 各场景实测

工具链：`agentgo-cli run -yolo -w <ws>`，全部核对 `AGENTGO_DUMP_LLM_DIR` 里的 `model` 字段
确认是 `qwen/qwen3.7-max`。

| 场景 | 结果 |
|---|---|
| 新建文件 + 读回确认 | 通过（3 行中文内容正确） |
| 多文件 Go 项目修 bug + 跑测试 | 通过（根因定位准确，未改测试，`go test ./...` 通过） |
| 中文目录 + 中文文件名读写 | 通过 |
| 大文件精确统计（2 万行） | 首轮把 100 当总数（bug 3）；修后正确得到 2141 |
| 相对路径 `path=.` grep | 修前 "No files found"，修后命中 |
| 越界搜索（工作区外） | 修前直接读；修后挂起等审批，`-yolo` 才放行 |
| 非 `-yolo` 审批流程 | 挂起→`approvals` 列出→`approve ID` 恢复执行，文件落地 |
| `-json` 事件流 | 正常，`status/token/done` 顺序正确 |
| `ask_user` 提问 | 正常挂起，`approvals` 列出，`deny` 可丢弃 |
| 会话续接 `-session` | 通过（前一轮记的代号能答出） |
| 3 工作区并发 | 修前 p2 失败（bug 4）；修后 3/3 成功 |
| 6 工作区并发压测 | 6/6 成功，无降级 |
| 办公文档基线 18 题 | 18/18 正确、18/18 带出处（37 次模型调用，74s） |
| Round4 probe（4 个沙箱任务） | 4/4 verified，0 stall |
| Round4 harvest（4 任务 × 2 次） | 8/8 verified |
| Round5 ghost（不存在软件，11 arms） | 全部 refusal，无反事实完成 |

模型自身的小毛病（不是 AgentGo 的 bug，供参考）：
- 会重复调同一个 `tool_search`（最多见过 4 次相同 query）。
- 生成正则时把 `code=9` 写成 `"code":\s*9`，连试 3 次才换对；此时已耗掉步数上限。
- 用 `run_batch_script` 跑 Python 时爱用 `os.walk('.')`，而 `.` 是 spawn 目录而非工作区，
  好在多数情况下 cwd 恰好一致。

## 验证方式

```powershell
go build ./...
go vet ./internal/codetools/ ./internal/sessions/ ./internal/bridge/
go test -count=1 ./internal/codetools/ ./internal/sessions/ ./internal/bridge/ `
                  ./internal/config/ ./internal/governance/ ./internal/tools/
```

新增回归测试：
- `internal/codetools/search_relative_test.go` — 相对路径解析 + 越界审批
- `internal/codetools/search_capped_test.go` — 截断必须在首行说明
- `internal/codetools/search_symbol_gate_test.go` — 符号搜索同样要走审批
- `internal/sessions/context_events_concurrent_test.go` — 多进程并发写
- `internal/bridge/config_apikey_test.go` — `AGENTGO_API_KEY` 覆盖优先级

## 全量回归（2026-11-01 10:00）

`go test ./internal/...` 覆盖 70 个包，除下面两处外全部 ok：

- `TestNativeLSPToolsAgainstGopls` 在一次全量并行里失败过一次，单独连跑 3 次全过；
  属于 gopls 子进程在机器满载时的偶发，与改动无关。
- `internal/agent` 在三包并行时撞到 15m 超时（跑了 904s）。单独跑两次均通过
  （214s / 262s）。并行时机器争抢导致，不是测试失败。

## 尚未覆盖的面（下一步可选）

1. **`cmd/*` 层几乎没有测试**：`agentgo-cli`、`agentgo-web`、`agentgo-re`、`llmtest` 都没有测试文件。
   本轮 CLI 的问题全靠手工实测发现。
2. **未处理审批阻塞整个运行时**：`workspaceBusyReason` 只要有任一挂起审批就拒绝所有工作区切换，
   报错文案指向 "workspace" 而不是"有审批没处理"。设计如此但容易误判，可考虑改进报错。
3. **`AZURE_OPENAI_*` 会静默顶掉用户配置**：`cmd/agentgo-cli/backend.go` 无条件覆盖，
   这是本轮最大的坑（浪费了整轮实测）。建议加一个"仅在未显式配置时启用"的判断，或在启动日志里打印生效的模型。
4. **桌面端（Wails GUI）未实测**：本轮只测了 CLI。共享同一 runtime，但 UI 事件、审批弹窗没验证过。
