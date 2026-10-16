# Crush 架构深度解构与消化路线

> **日期**: 2026-10-03  
> **目标**: 全面解构 Charm Crush (`github.com/charmbracelet/crush`) 源码架构，完成从“外部黑盒抓包猜测”到“内部白盒协议拉平与资产吸收”的转变，为 AgentGo 赋能顶级 TUI 身体与实时 LSP 诊断系统。

---

## 1. Crush 核心资产图谱

Crush 并不是一个简单的终端应用，而是一个高度模块化、工程质量极高的现代 AI 编程助手体系。在本地仓库（`../crush`）中，其核心架构分为 7 大资产域：

```
crush/
├── internal/
│   ├── ui/          # 【资产 1: 顶级 TUI 交互架构】
│   │   ├── chat/    # 消息流式渲染、思维链折叠、ANSI Diff 高亮、工具卡片
│   │   ├── model/   # UI 主状态机、侧边栏 (Sidebar)、LSP 状态监控、Landing 引导
│   │   └── styles/  # 基于 Lipgloss v2 的现代化色彩与排版系统
│   ├── workspace/   # 【资产 2: 双形态工作区核心抽象】
│   │   ├── workspace.go       # 核心 Workspace 接口 (UI 与后端的唯一契约)
│   │   ├── app_workspace.go   # 单进程内嵌模式 (直接持有 *app.App)
│   │   └── client_workspace.go# 网络解耦模式 (通过 HTTP/SSE 驱动远程 Server)
│   ├── proto/       # 【资产 3: 官方权威协议契约】
│   │   ├── message.go # 消息快照模型、ContentPart 多态定义、思维链序列化
│   │   ├── proto.go   # LSP 事件、工作区状态事件
│   │   └── requests.go# 所有 REST API 请求体定义
│   ├── lsp/         # 【资产 4: 基于 powernap 的语言服务器子系统】
│   │   ├── manager.go # LSP 进程懒加载、生命周期、文件类型路由
│   │   └── client.go  # JSON-RPC 2.0 客户端、诊断流式聚合、文件版本同步
│   ├── server/      # 【资产 5: 多客户端服务端】
│   │   ├── endpoints.go # 基于 apigen 的 /v1 REST 路由注册
│   │   ├── events.go    # SSE 事件发布订阅系统
│   │   └── backend/     # 客户端引用计数、工作区缓存、超时回收
│   ├── db/          # 【资产 6: 纯 Go SQLite 持久化体系】
│   │   ├── migrations/  # Goose 规范的 SQL 增量迁移脚本
│   │   └── sql/         # 会话、消息、修改文件快照 (Files 版本表)
│   └── agent/       # 【资产 7: LLM 编排与工具执行】
│       ├── coordinator.go # 协调器 (模型选择、Token 预算、上下文裁剪)
│       └── tools/         # 基础文件工具、Bash、LSP 工具、MCP 网关
└── pkg/
    └── codetools/   # 【已桥接资产: 零依赖工具箱】
        └── codetools.go # 导出 LSP + 文件工具，供外部 Agent (如 Eino) 复用
```

---

## 2. 核心契约解构：`Workspace` 接口

Crush 无论在终端全屏 TUI 模式，还是在 `crush run` 非交互命令行模式下，前端展示层与后端执行层都**只有这一个单一接口解耦**：

```go
// internal/workspace/workspace.go
type Workspace interface {
    // 1. 会话生命周期
    CreateSession(ctx context.Context, title string) (session.Session, error)
    GetSession(ctx context.Context, sessionID string) (session.Session, error)
    ListSessions(ctx context.Context) ([]session.Session, error)
    SaveSession(ctx context.Context, sess session.Session) (session.Session, error)
    DeleteSession(ctx context.Context, sessionID string) error

    // 2. 消息与历史
    ListMessages(ctx context.Context, sessionID string) ([]message.Message, error)
    ListSessionHistory(ctx context.Context, sessionID string) ([]history.File, error)

    // 3. Agent 驱动与控制
    AgentRun(ctx context.Context, sessionID, prompt string, attachments ...message.Attachment) error
    AgentCancel(sessionID string)
    AgentIsBusy() bool
    AgentIsSessionBusy(sessionID string) bool
    AgentModel() AgentModel

    // 4. 权限与交互提问
    PermissionGrant(perm permission.PermissionRequest) bool
    PermissionDeny(perm permission.PermissionRequest) bool
    PermissionSkipRequests() bool
    QuestionAnswer(responses []question.Answer) bool
    QuestionCancel() bool

    // 5. LSP 监控与管理 (前端侧边栏数据源)
    LSPStart(ctx context.Context, path string)
    LSPStopAll(ctx context.Context)
    LSPGetStates() map[string]LSPClientInfo
    LSPGetDiagnosticCounts(name string) lsp.DiagnosticCounts

    // 6. 事件广播订阅
    Subscribe(program *tea.Program)
    Shutdown()
}
```

### 关键设计启示：
1. **彻底解耦**：Bubbletea UI（`internal/ui/model/ui.go`）完全不知道底层的 Agent 是 Fantasy 还是 Eino。UI 内部只有一个 `com.Workspace` 指针。
2. **双模式切换原理**：
   - 当 `CRUSH_CLIENT_SERVER=1` 时，Crush 构造 `ClientWorkspace`，把每个方法转为 HTTP 请求，把 SSE 转为 Bubbletea `tea.Msg`。
   - 当本地运行时，Crush 构造 `AppWorkspace`，直接调用内部服务。
3. **AgentGo 归一化路径**：AgentGo 既可以在服务端假装成 Crush Server（当前 P1.7 路线），也可以在未来直接提供一个 `AgentGoWorkspace`，把原版 Crush UI **无缝嵌入为 AgentGo 的原生终端身体**！

---

## 3. 官方协议白盒化 (消除一切黑盒推测)

对比 `crush/internal/proto/message.go` 与 AgentGo 的 `internal/crushproto/wire.go`，我们确认了官方的数据流转规范：

### 3.1 消息多态模型 (ContentPart)
Crush 的每条消息包含一个 `parts` 数组，每个 part 序列化为包装结构：
```json
{
  "type": "text | reasoning | tool_call | tool_result | finish | image_url | shell_command",
  "data": { ... }
}
```

各 Part 的官方字段定义：
- **`reasoning` (思维链)**：
  ```json
  {
    "thinking": "string",
    "signature": "string",
    "started_at": 1791000000,
    "finished_at": 1791000005
  }
  ```
  *白盒事实*：UI 内置了三态思维链渲染机（折叠 `[click to expand]` -> F5 展开最后 200 行 -> 全量展开）。只要发射 `reasoning` part，TUI 就会自动渲染带有呼吸灯动画的思维框！
- **`tool_call` (工具调用)**：
  ```json
  {
    "id": "call_123",
    "name": "code_view",
    "input": "{\"path\":\"main.go\"}",
    "type": "tool_call",
    "finished": true
  }
  ```
- **`tool_result` (工具结果)**：
  ```json
  {
    "tool_call_id": "call_123",
    "name": "code_view",
    "content": "file content...",
    "metadata": "",
    "is_error": false
  }
  ```
- **`finish` (生成结束)**：
  ```json
  {
    "reason": "end_turn | tool_use | max_tokens | canceled | error | content_filter",
    "time": 1791000010
  }
  ```

---

## 4. LSP 子系统深度解构与侧边栏点亮方案

### 4.1 Crush 的 LSP 是如何运作的？
1. **底层引擎**：`github.com/charmbracelet/x/powernap`。内置了所有主流语言的 LSP 映射表（gopls, pyright, tsserver, rust-analyzer, clangd, ruff 等）。
2. **管理中枢**：`internal/lsp/manager.go`。
   - `NewManager(cfg)`：从配置加载并合并默认语言服务。
   - `Start(ctx, path)`：根据传入文件的扩展名或项目根标识（如 `go.mod`），懒加载启动对应 LSP 进程，通过 stdin/stdout 进行 JSON-RPC 2.0 通信。
   - `Client`：维护每个语言服务器的 `diagnostics`（按文件 URI 存储），维护 `DiagnosticCounts`（Error/Warning/Info/Hint 计数）。
3. **UI 刷新链路**：
   - TUI 每 5 秒（`lspStatesTTL`）或在收到 SSE `LSPEvent` 时，异步触发刷新：
     ```
     UI -> GET /v1/workspaces/{id}/lsps
        -> GET /v1/workspaces/{id}/lsps/{name}/diagnostics
     ```
   - 只要这两个接口返回真实结构，侧边栏的 `LSPs` 区块和顶栏的错误警告图标就会立刻渲染！

### 4.2 AgentGo 的吸收落地步骤
1. **打通 `Toolbox` 的 LSP 导出**：
   - 目前 `agentgo/internal/bridge/codetools_plugin.go` 已经启动了 `crush/pkg/codetools.Toolbox`。
   - 在 `pkg/codetools` 中暴露 `Toolbox.LSPManager() *lsp.Manager`。
2. **对接 `crushproto` 的 LSP 路由**：
   - 在 `internal/crushproto/routes.go` 中，将 `/v1/workspaces/{id}/lsps` 接入真实的 `lsp.Manager.Clients()`。
   - 将 `/v1/workspaces/{id}/lsps/{name}/diagnostics` 接入 `client.GetDiagnostics()`。
   - 当 Eino 的 `codetools`（如 `code_view`, `code_edit`）触碰文件时，自动触发 `lspMgr.Start(ctx, path)`。
3. **效果**：
   - 用户打开 Crush TUI，侧边栏显示：`● gopls (0E 0W)`；
   - 代码产生语法错误时，侧栏实时跳变：`● gopls (1E 0W)`，顶栏红色高亮！

---

## 5. 多模型与配置系统解构 (Catwalk & Provider)

### 5.1 Crush 模型切换机制
- Crush 使用 `charm.land/catwalk` 库管理模型清单。
- UI 弹窗按 `ctrl+l` 选模型时，会调用：
  ```
  POST /v1/workspaces/{id}/config/model
  ```
  请求体指定 `large` 或 `small` 模型与 Provider ID。
- 在 Client/Server 模式下，当前 AgentGo 返回了静态 fixture，导致 TUI 无法动态切换模型。

### 5.2 对接方案
- 将 AgentGo 的 `LLMConfig`（支持 Azure、OpenAI、Ollama、Claude 等）动态映射为 Catwalk/Crush 的 `Config` 与 `Providers` 数据。
- 当接收到 `POST .../config/model` 时，通知 AgentGo 的 `engine.Router` 切换底层 Eino Model 实例。

---

## 6. 消化执行路线表

| 序号 | 任务阶段 | 核心行动 | 预期收益 |
| :---: | :--- | :--- | :--- |
| **P1.8 ✅** | **LSP 子系统贯通** | 1. 在 `pkg/codetools` 暴露 `LSPManager`<br>2. `crushproto` 实现真实的 `/lsps` 与 `/lsps/{name}/diagnostics` 接口<br>3. 广播 `LSPEvent` 到 SSE 事件流 | **已达成 (2026-10-03)**: 原版 Crush TUI 落地页与侧边栏实时呈现 `● gopls E1`，修复后变 `● gopls` (0 错误，全绿) |
| **P1.9 ✅** | **思维链 (Reasoning) 渲染** | 1. `adk_run.go` 与 `ReasoningObserver` 捕获 Eino 思维流<br>2. `crushproto_turn.go` 双轨支持 `engine.EventReasoning` 与 `<think>...</think>` 标签流<br>3. 发射标准带 `started_at`/`finished_at` 的 `reasoning` part | **已达成 (2026-10-03)**: 原版 Crush TUI 获得可折叠展开三态思维链窗口、耗时统计与呼吸灯动画 |
| **P1.10 ✅**| **模型配置动态联动** | 1. `crushproto` 引入 `ModelProvider` 接口<br>2. 暴露动态 `/config`、`/providers`、`/agent`<br>3. 真实落地 `POST .../config/model` 与 `config_changed` SSE 广播 | **已达成 (2026-10-03)**: 真 TUI 实测:`ctrl+l` 选择器只列 AgentGo 配置的主/备模型,切换后 toast + 顶栏 + `config.json` 同步;他家 provider 分组来自 TUI 本地 Catwalk,服务端以 400 兜底 |
| **P2.0 ✅** | **内嵌终端身体 (单二进制)** | 进程内内存管道传输:`crush/pkg/embedtui.Run(ctx, handler, opts)` + `AppService.CrushHandler()` + `cmd/agentgo-tui` | **已达成 (2026-10-03)**: 真 TUI 实测:对话 + reasoning + `ctrl+l` 换模型正常;进程无监听端口、无子进程,`ctrl+c`→`y` 干净退出。详见 [p2-embedded-terminal.md](p2-embedded-terminal.md) |
