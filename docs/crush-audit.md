# Crush 审计(基线:upstream/main `bdcf796c`,2026-10-02,≈ v0.97.1 + 14)

> 复核了 v0.96.1 时的结论,在最新 main 上依然成立。命令见文末。

## 1. 许可证
- `LICENSE.md`:FSL-1.1-MIT(2025-2026 Charmbracelet)+ 其中包含的 MIT(2025-03-21~05-30 Kujtim Hoxha 早期代码)。
- 不商业化:内部使用 / 修改 / fork 均在 Permitted Purpose 内。必须保留版权声明与许可文本;不得用 Charm 商标。
- 每个版本发布满 2 周年后可按 MIT 使用。

## 2. 已有 client/server(sidecar 的基础)
- `crush server [--host --cwd --data-dir]`;默认 Windows npipe,也可 `tcp`。
- `internal/server/endpoints.go` 声明 **77 个端点**(`/v1/...`),`GET /v1/docs/openapi.json` 为 OpenAPI。
- sidecar 阶段用到的主要端点:
  - 生命周期:`/v1/health`、`/v1/version`、`/v1/control`、`/v1/workspaces`
  - 事件流:`GET /v1/workspaces/{id}/events`
  - 会话/消息:`/sessions`、`/sessions/{sid}/messages`、`/history`
  - Agent:`POST /agent`(发送 prompt)、`/agent/sessions/{sid}/cancel|summarize|shell`、`/prompts/queued|list|clear`
  - 权限:`/permissions/grant`、`/permissions/skip`;提问:`/questions/answer|cancel`
  - LSP:`/lsps`、`/lsps/{lsp}/diagnostics`、`/lsps/start|stop`
  - 配置:`/config/set|remove|model|compact|provider-key`

## 3. core / UI 耦合(fork 阶段才会用到)
| 项 | 结果 |
|---|---|
| core 反向 import `internal/ui` | 仅 `app/app.go`、`backend/backend.go`、`format/spinner.go`(另有 `cmd/*`) |
| core 中的 bubbletea/lipgloss/bubbles | `agent/agent.go`(1 处 lipgloss)、`app/*`、`backend/events.go`、`format/spinner.go`、`workspace/*` |
| `internal/ui` | 约 3.86 万行,TUI 独占,GUI 不复用 |

核心包行数(非测试):agent 15.1k、config 4.9k、db 2.4k、server 2.6k、backend 2.3k、shell 2.1k、workspace 2.0k、client 1.7k、proto 1.8k、lsp 1.4k、message 1.3k、skills 0.8k、hooks 0.6k、session 0.4k、question 0.3k、permission 0.3k、history 0.2k、filetracker 0.08k、diff 0.02k。

## 4. 对接要点
- `diff` 包只有 23 行(依赖 `go-udiff`),GUI 的 diff 不要依赖 Crush 渲染器,走 before/after + Monaco DiffEditor。
- 模型层是 `charm.land/fantasy`,AgentGo 是 Eino:不统一,AgentGo 单点写 `crush.json`。
- Go 版本:Crush go.mod 要求 go 1.27,AgentGo 本机已能构建两者。

## 5. 复核命令
```powershell
cd ..\crush
# core 依赖 ui
Get-ChildItem -Recurse -Filter *.go internal | ? { $_.FullName -notmatch '\\internal\\(ui|cmd)\\' -and $_.Name -notmatch '_test.go' } | Select-String 'crush/internal/ui' -List
# 端点数
(Select-String internal\server\endpoints.go -Pattern 'apigen\.(Get|Post|Put|Delete)\(').Count
```
