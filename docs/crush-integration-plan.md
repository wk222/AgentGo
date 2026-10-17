# AgentGo × Crush 融合方案(plan)

> **已确认决策(2026-10-03)**:① GUI 保持 **Vue**(`frontend-legacy`);② **先 sidecar 起步**;③ 不商业化,已 fork。
> **基线**:fork 为 `wk222/crush`(https://github.com/wk222/crush),本地 `..\crush`,
> remote:`origin`=fork,`upstream`=charmbracelet/crush;`main` 跟随 upstream/main(当前 `bdcf796c`,≈v0.97.1+14 commits,2026-10-02),
> AgentGo 专用改动放 `agentgo/*` 分支(已建 `agentgo/base`)。已用该版本编译成功,`crush server` 可用。
> 注:§0 的审计数据来自 v0.96.1,升到最新 main 后 Phase 0 需复核一遍(依赖已变:fantasy v0.45.1、catwalk v0.52.49 等)。
> 同步上游:`git fetch upstream; git checkout main; git merge --ff-only upstream/main; git push origin main`。
> 许可证:Crush 为 FSL-1.1-MIT;不商业化则自用/内部/研究均可(见 §8)。

## 0. 结论先行

方向成立:**不改造 Crush TUI,而是以 Crush 的 coding core 作为 AgentGo 的 CodingEngine,TUI 与 GUI 都只是前端。**

实测审计(v0.96.1)带来两个对方案有利的事实:

1. **Crush 已经是 client/server 架构**
   - `crush server` 子命令存在;`internal/server` 暴露 `/v1/...` HTTP API(OpenAPI 在 `/v1/docs/`),支持 `tcp` 与 Windows `npipe`。
   - 端点已覆盖:workspaces、sessions、messages、`/agent`(发送 prompt、cancel、summarize、queued prompts、shell)、`/permissions/grant`、`/questions/answer`、`/lsps/.../diagnostics`、`/filetracker`、`/workspaces/{id}/events`(事件流)、config/model 切换。
   - `internal/cmd` 的 TUI 本身就是该 server 的 client(`internal/client` + `internal/workspace/client_workspace.go`)。
2. **core 与 UI 的耦合很浅**
   - `internal/ui` 约 3.7 万行,是体量最大的一块,但 core 里反向 import `internal/ui` 的只有 `app/app.go`、`backend/backend.go`、`format/spinner.go` 和 `cmd/*`。
   - `internal/agent/agent.go` 仅有 1 处 lipgloss(第 1160 行,链接样式),可直接删。
   - core 规模:agent 1.46 万、config 4.7k、db 2.2k、shell 2.1k、lsp 1.4k、message 1.3k、skills 0.8k、hooks 0.6k、permission 0.3k、session 0.3k。

→ **因此不必一上来就 fork 拆分**。先走"sidecar + HTTP 客户端",验证价值后再决定是否 fork 进 monorepo。

## 1. 目标架构

```
                     AgentGo Core (Go)
   ┌───────────────────────────────────────────────────────┐
   │ Workflow Engine (Eino Graph/Compose)  Multi-Agent      │
   │ Memory / 冲突处理    CapabilityBus / 审计 / 审批        │
   │ Inner App            Taskhub / Kanban                  │
   │                                                       │
   │        EngineRouter  (统一 AgentEngine 接口)           │
   │     ┌─────────────┬──────────────┐                    │
   │  EinoEngine   CodingEngine(Crush)   ...               │
   └──────┬────────────┬───────────────┘                    │
          │            │                                    │
          │     crush server (sidecar → 后期可 in-proc)      │
          └────────────┴────────────────────────────────────┘
                 │ 统一事件总线 (EventSink)
      ┌──────────┼───────────────┐
   Crush TUI   AgentGo GUI     Headless
 (Bubble Tea)  (Wails)        agentgo serve / MCP
```

三个入口(最终形态):
- `agentgo` → TUI(初期直接是 Crush TUI,预注入 AgentGo MCP)
- `agentgo-gui` → Wails 桌面端
- `agentgo serve` → headless(工作流/CI)

## 2. 职责划分

| 层 | 归 Crush(吸收) | 归 AgentGo(保留) |
|---|---|---|
| Agent 循环 | coder 循环、工具调用、队列 prompt、取消、auto-summarize、loop detection | Eino ADK、Graph/Compose 多 Agent 编排 |
| 工具 | edit(含空白容错)、multiedit、view、grep/glob/ls、bash + job、LSP 工具、fetch/search | CapabilityBus 统一注册、Inner App 作为工具 |
| 代码智能 | LSP manager、diagnostics 回灌、filetracker、history(文件版本) | — |
| 会话 | session/message(SQLite、33ms 去抖流式) | 跨引擎会话索引、工作流 run 记录 |
| 权限 | permission、question、hooks | governance 审批队列、审计(上层统一) |
| 记忆 | — | 长期记忆、冲突处理、上下文注入 |
| UI | Bubble Tea TUI(保留作为一个前端) | Wails GUI、可视化工作流、Workspace 管理 |

**关键张力(必须正视)**:Crush 用 `charm.land/fantasy` 做模型抽象,AgentGo 用 Eino。两套 LLM 栈会并存。
决策:**不强行统一**。Coding 会话由 Crush 持有模型调用;AgentGo 工作流里新增 `CodingAgent` 节点,通过 `CodingEngine` 调 Crush 完成"一个编码任务",拿回结果 + 变更文件集。模型/Key 配置由 AgentGo 单点写出 Crush 的 `crush.json`,避免用户配两遍。

## 3. 分阶段计划

### Phase 0 — 冻结基线(0.5 天)
- AgentGo 现有功能回归清单:桌面启动、终端 PTY、聊天、工作流 run、记忆召回。写成可跑的 smoke test / 脚本。
- 记录 Crush `v0.96.1` 依赖与许可证审计表(`docs/crush-audit.md`):可复用包、必须丢弃包(`internal/ui`、`login`、`logout`)。
- 验收:`go test ./internal/bridge/...` 与 smoke 脚本在当前 HEAD 通过。

### Phase 1 — Sidecar:零 fork 跑通 TUI + 桥(1–2 天)
- 已有:`crush.exe`、`agentgo-mcp.exe`。
- 做:
  - 提供 `scripts/agentgo-tui.ps1`:生成/合并 `.crush/crush.json`(注入 agentgo MCP + `AGENTGO_WORKSPACE_ROOT`),再启动 `crush`。
  - 补 MCP 工具:`agentgo_memory_recall/save`、`agentgo_task_*`(taskhub)、`agentgo_app_*`(Inner App)。
  - Crush 上下文文件(`AGENTS.md`/`CRUSH.md`)里写明何时调用 agentgo 工具。
- 验收:在 `crush` TUI 中让模型"查记忆 → 跑工作流 → 回写结果"一条链路通。

### Phase 2 — 统一运行时接口(2–3 天)—— ✅ 已完成(2026-10-03)

> 实现:`internal/engine`(types/emitter/router + 测试,含 `-race`)、`bridge/engine_eino.go`、`bridge/engine_ipc.go`;
> `internal/arch` 登记为 LayerSystems。IPC:`RunEngine`(异步,事件 `engine:event`)、`RunEngineSync`、
> `CancelEngineRun`、`CancelEngineSession`、`ListEngines`、`ListActiveEngineRuns`。
> **偏离计划**:现有 Vue 聊天(`SendMessageStream`)暂未切到 router,保持零回归;新通道并行,M4 做 GUI 时再迁移。

#### 原计划内容
在 `agentgo/internal/engine` 定义(只定义小而稳的接口):

```go
type AgentEngine interface {
    ID() string
    Run(ctx context.Context, req RunRequest, sink EventSink) (RunResult, error)
    Cancel(runID string) error
}
type EventSink interface { Emit(Event) } // token / tool_call / tool_result / approval / diff / status
type RuntimeServices struct { /* workspace, memory, governance, taskhub ... */ }
```

- `EinoEngine`:包裹现有 `bridge.Runtime` 的 agent runner(行为不变,只加适配层)。
- 统一事件 schema(JSON),Wails Events / SSE / MCP 通知共用。
- 验收:现有 Vue 聊天经 `EngineRouter` 走 `EinoEngine`,回归通过。

### Phase 3 — CodingEngine(Crush sidecar 适配)(3–5 天)—— ✅ 已完成(2026-10-03,配置同步与审批 UI 除外)

**实现与偏差**:
- `internal/engine/crushengine`:手写的精简 HTTP/SSE client(只用到约 10 个端点,未走 OpenAPI 生成);Supervisor 负责子进程(随机端口、`/health`、死亡自动重启、Windows 下 taskkill /T 清理进程树)。
- SSE 的 message 是**快照**,翻译层按文本差分出 token/reasoning 增量;同一条消息内先出文本增量,再出 tool_call;文件事件靠版本号(v0 为基线)出 before/after。
- 取消语义:提示词在途时取消 → 尽力通知 Crush 取消并返回 `cancelled`(而不是报错)。
- bridge 里 opt-in 注册(`engine_crush.go`):`AGENTGO_CRUSH_EXE`(或 app 旁 / `..\crush\`)、`AGENTGO_CRUSH_CONFIG_DIR`、`AGENTGO_CRUSH_AUTOAPPROVE`(默认 1;`0` = 拒绝所有工具权限,即只读,待 M4 审批 UI)。`ServiceShutdown`/`Close` 清理 sidecar。
- 工作流 `coding_agent`(别名 `crush`)节点:Config 支持 `work_dir`/`engine`/`session_id`;输出 JSON `{summary, changed_files[{path,before,after}], tool_calls, engine}`,并写入变量 `<节点ID>.summary`。注意节点 **id 不能叫 `start`/`end`**(Eino 保留字),用 `type` 区分。
- 验收(真实 Crush + Azure `gpt-6-luna`,默认跳过,`AGENTGO_CRUSH_E2E=1` 开启):
  - `crushengine.TestE2EFixCompileError`:Router → Crush,≈16 s 修复编译错误。
  - `bridge.TestE2ECodingAgentWorkflow`:工作流节点 → AppService → Router → Crush,≈17 s,返回 main.go 的 before/after。**务必设 `XDG_DATA_HOME` 为临时目录**,否则会写你真实的 AgentGo 数据库。

**未做 / 推迟**:AgentGo LLM 配置 → `crush.json` 同步(目前由 `AGENTGO_CRUSH_CONFIG_DIR` 指向独立配置);Approver 接入 AgentGo 审批队列;`unified_diff` 暂为空;`RunRequest.Images` 被忽略;Windows 上主进程被强杀时 sidecar 可能残留(后续可用 Job Object)。

原计划:
- `internal/engine/crushengine`:
  - 子进程管理:`crush server --host npipe/tcp 127.0.0.1:随机端口`,健康检查 `/v1/health`,崩溃重启,退出时清理。
  - 基于 `/v1/docs/openapi.json` **生成 Go client**(不 import Crush internal,绕开 internal 限制)。
  - 订阅 `/v1/workspaces/{id}/events`,映射为统一 `Event`。
  - 实现 `Run`(→ `/agent`)、`Cancel`、`summarize`、权限回调(→ `/permissions/grant`,接入 AgentGo 审批队列)。
- 配置同步:AgentGo LLM 配置 → 写 `crush.json` provider/model。
- 工作流节点:`CodingAgent` 节点(入参:任务描述、允许工具、工作目录;出参:摘要、变更文件、diff)。
- 验收:一个"读 Go 文件有语法错误 → LSP 诊断 → 自动修复"的端到端测试,经 AgentGo API 触发,结果可复现。

### Phase 4 — GUI 编程界面(Agent-first)(1–2 周)
前端技术:**保持 Vue**(见 §6),组件:
- Monaco Editor + **Monaco DiffEditor**(用 Crush 的 before/after/patch 数据,不搬 DiffView 渲染器)。
- xterm.js(已有 PTY 后端)。
- Vue Flow(已有)做工作流画布。
- 面板:Project(文件树)、Agent(会话/上下文占用/工具数)、Run Inspector(工具调用流 + Stop/Approve)、Problems(LSP diagnostics)。
- 数据来源:全部走统一事件流 + `CodingEngine` 的 REST。
- 验收清单:对话 → 工具调用可见 → 改动以 Monaco diff 展示 → Approve/Reject → 文件落盘 → Problems 更新。

### Phase 5 — 决策点:是否 fork 进 monorepo
仅当 sidecar 出现明确瓶颈时才做,触发条件示例:
- 需要同进程共享 AgentGo 记忆做 system prompt 动态注入(目前可用 MCP/上下文文件替代);
- 需要把 AgentGo CapabilityBus 工具直接注册进 Crush agent 工具表(目前可用 MCP 替代);
- sidecar 启动/IPC 延迟或打包体积不可接受。

若 fork:
```
agentgo/
  core/        (从 crush 抽出:agent, message, session, tool, lsp, permission, history, filetracker, shell, config, db)
  ui/tui/      (crush/internal/ui,保留)
  ui/gui/      (Wails 前端)
  cmd/agentgo, cmd/agentgo-gui
```
- 第一刀:删除 core 对 `internal/ui` 的 3 处依赖(`app.go`、`backend.go`、`format/spinner.go`)与 `agent.go` 的 lipgloss;改 module path;保留 LICENSE 与版权声明(FSL 要求)。
- 之后定期对 upstream 打 patch,控制 diff 范围。

### Phase 6 — Headless `agentgo serve`
复用 EngineRouter + 统一事件流,暴露 HTTP/SSE;工作流与 CodingEngine 皆可在 CI/服务器运行。

## 4. 从 OpenSumi 借鉴什么(只借 UI 思路,不引入依赖)

OpenSumi 前端体验差的主因是重(webpack + DI + 大量 RPC 桥)。借鉴项:
- 布局模型:左(Explorer)/中(Editor+Diff)/右(AI 面板)/下(Terminal·Problems·Output)。
- 把 AI 面板拆成"会话 / 上下文 / 工具调用时间线 / 待审批"。
- ACP/事件语义:session update、tool call、permission request 的命名,统一事件 schema 时参考(我们已有 `opensumi-acp-workflow.md`)。
- 不借:DI 容器、模块注册体系、其 Monaco 封装。

## 5. 统一事件 schema(草案)

```json
{ "run_id":"", "session_id":"", "engine":"coding|eino",
  "type":"token|reasoning|tool_call|tool_result|file_change|approval_request|status|error|done",
  "payload":{} }
```
`file_change` 携带 `path / before / after / unified_diff`,GUI 直接喂 Monaco DiffEditor。

## 6. 决策记录(已全部确认:Vue / sidecar / 不商业化,原始讨论如下)

1. **GUI 框架:Vue(保持) vs React**
   你方案里写的是 React;但当前已有 Vue 3 + Naive UI + Vue Flow + Monaco,且你刚刚选择切回它。Monaco/xterm 与框架无关,**默认保持 Vue**,避免重写。若你坚持 React,需要单独立项,不建议和 Crush 集成同时做。
2. **起手方式:Phase 1/3 sidecar(推荐) vs 直接 fork**
   sidecar 一周内可见结果、无 internal 限制、升级简单;fork 控制力强但维护成本高。
3. **是否商业化**:决定 Phase 5 的 fork 是否可接受(见 §8)。

## 7. 风险与对策

| 风险 | 对策 |
|---|---|
| fantasy 与 Eino 双 LLM 栈,配置分裂 | AgentGo 单点写 `crush.json`;CodingAgent 以节点形式隔离 |
| Crush server API 随版本变化 | 锁 `v0.96.1`;client 由 OpenAPI 生成,升级时重新生成 + 契约测试 |
| 权限双层(Crush permission + AgentGo governance) | Crush 请求→转发 AgentGo 审批队列,单一入口;可配置"Crush 全部放行,由 AgentGo 决策" |
| Windows 子进程/npipe 稳定性 | 初期用 tcp 127.0.0.1 随机端口 + 超时/重启;日志落盘 |
| 打包体积(再加一个二进制) | 可接受;Crush 单文件;Monaco workers 已是体积大头 |
| 国内/代理环境 | 沿用现有代理规则,`crush` 子进程继承 AgentGo 的环境 |

## 8. 许可证

- FSL-1.1-MIT:允许使用、修改、内部使用;禁止把软件(含修改版/派生)作为 **Competing Use** 的商业产品发布(替代 Crush 或提供同等功能);每个版本满 2 周年自动转 MIT。
- 自用/研究/内部:无问题。
- 若将来商业发布"AgentGo GUI Coding Agent":sidecar 模式也属于分发 Crush 二进制,同样受条款约束;需要律师/作者确认,或只依赖已满 2 年转 MIT 的旧版本。
- 无论哪种方式:保留 LICENSE 与版权声明,不使用 Charm 商标。

## 9. 里程碑与验收

| 里程碑 | 产出 | 验收 |
|---|---|---|
| M0 | 回归基线 + 审计文档 | smoke 脚本全绿 |
| M1 | TUI+MCP 脚本、扩展 MCP 工具 | TUI 内完成 记忆→工作流 链路 |
| M2 | `internal/engine` 接口 + EinoEngine | 现有聊天无回归 |
| M3 | CrushEngine sidecar + CodingAgent 节点 | LSP 自愈端到端测试通过 |
| M4 | GUI Agent-first 面板 + Monaco Diff + 审批 | 完整编码流程在 GUI 完成 |
| M5(可选) | fork / headless serve | 按 §3 Phase 5 触发条件 |

## 10. 下一步(立即可做)

1. 确认 §6 三个决策点。
2. 开 M0:补回归 smoke + `docs/crush-audit.md`。
3. 同步开 M1:`agentgo-tui.ps1` + 扩展 MCP 工具;它不依赖任何决策,马上可用。

## 2026-10-17 更新：Crush 源码并入 AgentGo

- 原来 `go.mod` 里 `replace github.com/charmbracelet/crush => ../crush`，而 fork 里的 `pkg/codetools`、`pkg/embedtui` 一直是**未跟踪文件**（`?? pkg/`），`internal/client/client.go` 也有未提交修改——也就是说只有这一台机器能编译 AgentGo。
- 现在这 73 个包（约 10 万行，含 LSP、代码工具、会话/db、MCP、oauth 和终端 UI）复制到 `third_party/crush/`，导入路径改写为 `agentgo/third_party/crush/...`，`go.mod` 不再依赖外部 crush 模块；全新 `git clone` 即可 `go build ./...`。
- 来源、许可（FSL-1.1-MIT，有“竞争性使用”限制）、刷新办法见 `third_party/crush/NOTICE.md` 与 `scripts/vendor-crush.py`。
- 这一步只是**把依赖变成自己的代码**，没有改变架构：`crushproto`（协议适配）和 `cmd/agentgo-tui`（原版 Crush TUI 当终端身体）都还在。后续要不要继续“原生化”（去掉用不到的 oauth/mcp/db、让 TUI 直接走进程内 `core` 而不是 HTTP 协议），是下一个独立决定。
