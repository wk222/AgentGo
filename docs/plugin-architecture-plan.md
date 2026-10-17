# AgentGo 插件化架构 Plan(桌面端 + TUI + 无头,一个内核)

> 目标:AgentGo 内核只保留"最小 agent 循环 + 服务表 + 插件宿主",其余(引擎、工作流节点、工具、记忆、看板、调度、Crush、UI 面板)都是插件。
> 桌面端(Wails + Vue)、TUI(Crush)、无头(`agentgo-mcp` / 将来的 `agentgo serve`)只是同一个内核的三个"身体"。
> 参考:`deepseek-harness-desktop`(dsh-tauri)。它的核心是 cordis 风格的**服务注入 + 作用域清理 + 事件中间件**,而不是"一份 manifest"。

关联文档:`crush-integration-plan.md`(Crush 集成,M0–M3 已完成)。

---

## 1. 从 dsh 学什么 / 不学什么

学(五个机制):

| # | dsh 机制 | AgentGo 对应 |
|---|---|---|
| 1 | 具名服务 + `inject` 依赖声明,框架排启动顺序 | `plugin.Host` 服务表;`Plugin.Inject/Optional/Provides`;拓扑排序 |
| 2 | `ctx.effect(fn)` 统一清理,卸载/热重载一次释放 | `Scope`(逆序 dispose、幂等、panic 隔离) |
| 3 | 带 `next` 的事件中间件(`tools/execute`) | `ctx.On(event, func(payload, next))` |
| 4 | host/client 两半;UI 经 slots;能力探测不猜版本 | host=Go/sidecar;client=Inner App bundle + Vue 槽位表 |
| 5 | 壳只管生命周期(装/升/卸、档案) | `agentpack` 增加 `plugin` 类型;档案=启用的插件集合 |

不学:
- 壳补丁 / DOM 补丁两级降级:dsh 是第三方核心才需要。AgentGo 自有内核,**直接留槽位**。
- Node + pnpm + 首启联网下载运行时:AgentGo 是 Go 单二进制。
- 许可:dsh 为 MIT + 非商用条款,只借鉴设计,不复制代码。

## 2. 核心契约(P0)

```go
type Plugin struct {
    Name     string
    Provides []string // 本插件会提供的服务(Apply 内必须真的 Provide,否则启动失败)
    Inject   []string // 必需服务;缺失 => Skipped
    Optional []string // 可选服务;参与顺序与级联重载
    Apply    func(*Context) error
}
```

`Context` 提供:`Effect` / `Contribute(kind,name,dispose)` / `Provide` / `Service`+`Use[T]` / `On` / `Emit`。

规则(照抄 dsh 的好习惯):
- **只能取已声明的依赖**,未声明即报错 => 依赖图永远是真的。
- 所有注册都挂在插件的 `Scope` 上;`Stop/Reload` 先级联停掉依赖者,再逆序释放。
- 单个插件失败**不拖垮整机**:状态记为 `failed/skipped`,可通过 `Statuses()` 观察。
- **治理自动生效**:`Contribute` 触发 `Hooks.Contributed/Retired`,由 bridge 接到 `capability.Bus`,
  于是每个插件的每项贡献自动有 grant、审计、风险等级,不用各插件自己接。
- `internal/plugin` 只依赖标准库(`LayerFoundation`);用 `internal/arch` 再加一条:**核心包不得 import 任何插件包**。

## 3. 两类插件,一个契约

- **进程内插件**(一方、编译进二进制):Eino 引擎、记忆、看板、调度、工作流节点集…
- **进程外插件**(`plugin.json` + sidecar,MCP/HTTP):Crush、第三方。
  Host 读 manifest,生成**代理插件**,由代理代它向服务表注册引擎/节点/工具/面板。

```jsonc
// plugin.json(进程外插件;进程内插件用 Go 结构体声明同样的字段)
{
  "name": "crush",
  "version": "0.1.0",
  "inject": ["engines", "workflow.nodes"],
  "runtime": { "kind": "sidecar", "exe": "${AGENTGO_CRUSH_EXE}", "health": "/v1/health" },
  "contributes": {
    "engines": [{ "id": "crush" }],
    "workflow_nodes": [{ "type": "coding_agent", "aliases": ["crush"], "engine": "crush" }],
    "panels": []
  },
  "governance": { "risk": "high", "approval": "auto|ask" }
}
```

## 4. 三个身体

```
                    ┌────────── AgentGo Core(Go)──────────┐
                    │  plugin.Host  +  服务表  +  事件总线     │
                    │  Router / NodeRegistry / ToolRegistry   │
                    │  Memory / Approval / Sessions / LLM     │
                    └───────▲───────────▲───────────▲─────────┘
              Wails IPC/Events│        MCP stdio│       HTTP/SSE(将来)
                  桌面端(Vue)│          TUI(Crush)│     无头 agentgo serve
```

- 桌面端:Vue 前端按槽位表挂载插件面板。
- TUI:Crush 经 `agentgo-mcp` 使用内核;同时 Crush 本身又是一个**引擎插件**(双向)。
- 无头:同一 Host,不装 UI 插件即可。

## 5. 里程碑

| 里程碑 | 内容 | 验收 |
|---|---|---|
| **P0 ✅** | `internal/plugin`:Scope、事件中间件、服务表、Host(拓扑排序、失败隔离、级联停止/重载、观察者钩子) | 单测(含 `-race`):装卸/顺序/循环/缺依赖/级联/热重载/中间件。已完成(2026-10-03),16 个用例 |
| **P1 ✅** | 现有注册点接入服务表:引擎 Router(`Register` 返回 dispose)、节点扩展表 `workflow.RegisterNodeExecutor`、工具 `Registry`(已有 Add/Remove)作为核心服务发布;bridge 把 Hooks 接到 `capability.Bus`;`eino` 与 `crush` 改为一方插件;`coding_agent` 从内置节点移出,由 crush 插件贡献;新增 IPC `ListPlugins/ReloadPlugin/StopPlugin` | 已完成(2026-10-03):全量回归绿;grant 随装卸 published→retired→published;真实 Crush 工作流验收仍通过 |
| **P1.5 ✅** | codetools 一方插件:Crush 的 LSP 代码工具(`view/edit/multiedit/write/glob/grep/lsp_*`)在进程内适配成 Eino 工具,经 `Contribute("tool")` 进注册表;插件可声明工具"静态可见 + 是否改文件"(`tools.RegisterStaticTool`),只读画布自动隐藏会改文件的工具 | 已完成(2026-10-03):真 Eino agent + 真 Azure 模型 + 真 gopls 修复编译错误通过(`AGENTGO_CODETOOLS_E2E=1`),全量回归绿,MCP 冒烟 ALL OK |
| **P1.6 ✅** | `crushproto`:AgentGo 冒充 Crush server,原版 Crush TUI 以 AgentGo 为后端(探针,脚本化 Runner) | 见 §5.2:真 `crush run` 与原版 TUI 均通过,0 个 501 |
| **P1.7 ✅** | 真 Runner:Eino → `crushproto`(`crushprotoPlugin`,`AGENTGO_CRUSHPROTO_ADDR` 开启;`cmd/agentgo-headless` 无头宿主) | 真 `crush run` 与原版 TUI 驱动 Eino+codetools 修好编译错误(见 §5.3) |
| **P1.8 ✅** | 深度消化 Crush:LSP 子系统全链路贯通(白盒化 `internal/lsp`，导出 `pkg/codetools`，`crushproto` 接入 `/lsps` 与 `/lsps/{name}/diagnostics` + SSE `lsp_event`) | 原版 TUI 落地页与侧边栏实时呈现 `● gopls E1`，代码修复后变 `● gopls` (0 错误，全绿)(见 §5.4) |
| P2 | 进程外插件加载:`plugin.json` → 代理插件;Crush 迁成插件,删除 `engine_crush.go` 硬编码 | M3 两条真实验收(Router 与 工作流)仍通过 |
| P3 | 前端槽位注册表 + manifest `panels`;Agent 面板作为首个插件面板(= M4 的前置) | 面板随插件装卸出现/消失 |
| P4 | 记忆、看板、调度逐个迁成一方插件;`arch` 规则"核心不 import 插件" | 每迁一个回归全绿 |
| P5 | `agentpack` 支持 `plugin` 类型(安装/升级/卸载);档案=启用集合 | 导出/导入一个插件包 |

M4(GUI)基于 P3 之后再做;原 `crush-integration-plan.md` 的 Phase 4/5 顺延。

## 5.1 codetools 落地经验(2026-10-03,铁律:踩过的坑别再踩)

**依赖合并**
- agentgo 现需 `go 1.27.0`(随 Crush);**2026-10-17 起 Crush 源码已内置到 `third_party/crush`(见其 NOTICE.md),不再有 `replace ... => ../crush`**;
  `replace go.opentelemetry.io/contrib/.../otelgrpc => @v0.59.0`,因为 milvus→etcd/server v3.5.10 用了新版已删除的 otelgrpc 拦截器。
  `agentgo-crush` 只需 `replace agentgo => ../agentgo`、otelgrpc 那条 replace 和 go 1.27.0(crush 的 replace/require 已删);**agentgo 依赖变动后要在 agentgo-crush 里 `go mod tidy`**,否则 `agentgo-mcp` 构建报 "updates to go.mod needed"。
- sonic 在 go1.27 退化为 encoding/json(仅性能提示)。

**"工具注册了 ≠ agent 看得见"——三层才算通**
1. 注册表有(`Contribute("tool")`)。
2. 静态可见:否则只能靠 `tool_search` 发现,模型常常不去搜 → 加 `tools.RegisterStaticTool(name, mutating)`;模式白名单 `StaticToolAllowlist` 会自动放行(Focused 画布隐藏 mutating)。
3. 治理风险表有条目(`governance/policy_build.go`)。

**排查"agent 说没有工具"的真凶(三连)**
- `sendMessageCore` 里 `runner.Generate` 失败会**静默回退到不带工具的 `ChatOnce`**,模型于是答"没有这些工具"。现已打日志 `[chat] agent run failed …`。**先看这条日志,别先怀疑工具没注册。**
- 推理模型部署(如 Azure gpt-5/o 系列)在 `/chat/completions` 上带函数工具必须 `reasoning_effort=none`,否则 400。已加可选环境变量 `AGENTGO_REASONING_EFFORT=none`(默认不变)。长期方案:走 Responses API 或把该项并入 LLMConfig。
- **工具结果不能为空**:空 tool 消息会被下游丢弃,assistant 的 `tool_calls` 失去响应,下一次请求 400。适配器已用 `(no output)` 兜底,有回归测试。
- 调试利器:`AGENTGO_DEBUG_LLM=1` 只打印发给模型的消息"形状"(角色/tool_call id/工具名,无密钥无正文),一眼看出哪个 tool_call 没有响应。

**E2E 纪律**:每次用全新 `XDG_DATA_HOME`。复用数据目录会让上一轮残留的会话历史污染下一轮,造成"时好时坏"的假象(本次就因此多绕了一圈)。

**已知遗留**
- Crush 的 `lsp_diagnostics` 只覆盖 LSP 已打开的文件;首次调用可能"无输出"(现显示为 `(no output)`),不等于"无错误"。需要在适配层让它先打开工作区文件,或在提示词里要求先 `code_view`。
- codetools 的工具箱在加载时绑定工作区;工作区切换后需要 `ReloadPlugin("codetools")`,尚未自动联动。
- codetools 的 Approver 默认放行,未接 AgentGo 审批队列(治理风险等级按工具名生效)。
- 两套 LLM 配置仍未统一(AgentGo → crush.json 同步)。

**下一步(按序)**
1. 工作区切换自动 `ReloadPlugin("codetools")`;codetools 的 Approver 接入 AgentGo 审批队列。
2. 修 `lsp_diagnostics` 首次为空的问题(预热/先打开文件)。
3. LLMConfig 增加 reasoning_effort 一等字段(或 Responses API),去掉环境变量变通。
4. 统一配置:AgentGo LLM 配置 → `crush.json`,让 Crush TUI 与 Eino 同源。
5. P2(`plugin.json` 进程外加载 + Job Object 收口 sidecar)→ P3(前端槽位)→ GUI(M4)。

## 5.2 冒充 Crush server 探针(2026-10-03):结论 = 可行

**做了什么**:`internal/crushproto` 实现 Crush 的 `/v1` 协议(约 60 条路由,静态响应来自从真 server 录制的夹具,动态部分:工作区/会话/消息/SSE 事件/权限审批/取消),`cmd/crushproto-probe` 启动它。协议层(`Server`)与"谁来跑 agent"(`Runner` 接口)分离;探针用脚本化 `ScriptRunner`(不调模型)。

**验收(全部通过)**
- 协议级测试 `-race`:批准 / 拒绝 / 取消中途 / 忙碌拒绝 / 未知路由 501 / 夹具不含密钥。
- 真 `crush run -H tcp://…` 作为客户端:打印出脚本回答(`AGENTGO_CRUSHPROTO_E2E=1` 门控测试)。
- **原版 Crush TUI**(不改一行)连上:渲染 Logo、模型栏、用户消息、工具调用、带 diff 的权限弹窗、流式文本、完成状态、侧边栏标题与 token 用量;启动期约 20 个端点全部 200,**0 个 501**。

**用法**
```
go run ./cmd/crushproto-probe -addr 127.0.0.1:18770
$env:CRUSH_CLIENT_SERVER = "1"; crush -H tcp://127.0.0.1:18770
```

**协议要点(录制所得,别再猜)**
- 必须设 `CRUSH_CLIENT_SERVER=1`,TUI 才走 `ClientWorkspace`;`-H tcp://host:port`。
- 版本不一致时客户端会发 `POST /v1/control`(`shutdown_if_idle`)想换掉服务器;**服务器有最后发言权**,返回 409 即继续使用我们。
- SSE 信封:`{"type":kind,"payload":{"type":"created|updated","payload":<实体>}}`;kind 有 `message / session / permission_request / permission_notification / file / agent_event / run_complete / update_available`。
- **消息是整体快照**:每个 `updated` 带完整 `parts`(`reasoning / tool_call / text / finish`);工具结果是另一条 `role:"tool"` 的消息;回合以 `agent_event(agent_finished)` + `run_complete(run_id,message_id,text)` 结束。
- `crush run` 创建会话时标题为 `non-interactive`,服务端在回合结束时替换成真标题。
- 权限:`permission_request` 事件 → 客户端 `POST …/permissions/grant {action: allow|allow_session|deny, permission}` → 服务端回 `{"resolved":bool}`。
- 错误体统一 `{"error":"…"}`;`client_id` 必须是 UUID。
- `project/needs-init` 固定返回 false,否则 TUI 会弹 AGENTS.md 初始化对话框(AgentGo 自己管项目上下文)。

**工具链(`scripts/crushproto/`,升级 Crush 后可整套重录)**
`record.ps1`(真 server + 录制代理 + 自动批准 + 抓夹具)、`rec_proxy.py`、`capture_fixtures.py`、`summarize.py`、`oapi.py`(查 OpenAPI)、`drive_tui.py`(用 pywinpty+pyte 驱动真 TUI 并读屏;依赖装在 `D:\dev\pytools`,`PYTHONPATH` 指过去)。

**安全坑(已修)**:`POST /v1/workspaces` 的请求/响应里带**整份进程环境变量**(含 API Key)。录制代理与夹具抓取脚本现在默认剔除 `env`;夹具测试断言不含 env 与 `sk-`。早先未脱敏的录制文件已删除。

**还没做(探针范围之外)**
- `ScriptRunner` → 真 Runner:把 AgentGo(Eino)事件翻译成 Crush 消息快照。已有 Emitter 把"agent 在做什么"翻成快照事件,Runner 只需调用它;AgentGo 的审批队列映射到 `RequestPermission`。
- 流式文本目前每个词一次整体快照,长回复要节流合并。
- 会话/消息现为内存态;需接 AgentGo `sessions` 存储(TUI 重启后可恢复会话)。
- `reasoning` 内容、图片附件、`/summarize`、shell 命令、`filetracker/lastread`、`lsps/{x}/diagnostics`、MCP 全家仍是桩(TUI 触发才会暴露,看日志里的 501)。
- 配置:`workspace`/`providers`/`agent` 夹具是 Azure 单模型的静态拷贝;需从 AgentGo LLMConfig 合成,模型切换(`config/model`)要落到 AgentGo。

## 5.3 真 Runner:Eino 经 crushproto 驱动原版 Crush(2026-10-03)

**结论**:原版 Crush(`crush run` 与 TUI)作为"身体",AgentGo 的 Eino agent + codetools 作为"大脑",修好了一个真实编译错误(Azure `gpt-6-luna`,约 14 秒)。

**组成**
- `internal/agent/tool_observer.go`:按运行绑定的 `ToolObserver`(ctx 传递),`ToolCallEventMiddleware` 用 `tc.CallID` 上报工具的 id/参数/输出。`EinoEngine.Run` 注入适配器,**Eino 引擎现在会发 `tool_call` / `tool_result` 引擎事件**(桌面端也可复用)。
- `internal/bridge/crushproto_turn.go`:引擎事件 → Crush 消息模型(assistant 含 text+tool_call;每个结果一条 `role:tool`;最后一个结果后 assistant 以 `tool_use` 收尾;文本增量 60ms 合并;`finish()` 后丢弃迟到事件)。
- `internal/bridge/crushproto_runner.go`:调用 `runEngineTurn`(走 `engine.Router`);Crush 会话懒映射为 AgentGo 会话(桌面端可见);待审批 → `RequestPermission` 阻塞等 TUI 回答 → `ResolveApproval` 恢复;ask_user 问题以文本展示。
- `internal/bridge/crushproto_plugin.go`:**默认关闭**;`AGENTGO_CRUSHPROTO_ADDR` 设了才监听,**只允许回环地址**(协议无认证),`AGENTGO_CRUSHPROTO_YOLO=1` 自动放行 TUI 弹窗,`AGENTGO_CRUSHPROTO_ENGINE` 选引擎。
- `cmd/agentgo-headless`:无头宿主(`-workspace -addr -yolo -engine`);设了 `AZURE_OPENAI_*` 则覆盖 LLM 配置。

**用法**
```
go build -o agentgo-headless.exe ./cmd/agentgo-headless
.\agentgo-headless.exe -workspace D:\proj -addr 127.0.0.1:18771
$env:CRUSH_CLIENT_SERVER = "1"; crush -H tcp://127.0.0.1:18771
```

**验收**
- 单测:`crushproto_turn_test.go`(消息结构 / 合并 / 结束后忽略 / 引擎事件映射 / 适配器)、`tool_observer_test.go`。
- E2E(门控 `AGENTGO_CRUSHPROTO_E2E=1`,需 `XDG_DATA_HOME` 指临时目录):`TestE2ECrushClientDrivesEino`,真 `crush run` → Eino 修好 `main.go`。
- 真 TUI(`drive_tui.py`)走完整一轮,文件被修好,工具与最终文字均渲染;侧栏显示 `Modified Files: main.go +1 -1` 与上下文用量。
- HTTP 级真模型 E2E:`TestE2ECrushprotoStateAfterFix` 断言"每个工具调用都有同 id 的结果 / history 含 main.go 改前改后 / 会话 token>0"。

**5.3.1 教训(别再踩)**
- **Eino ADK 传给 `WrapInvokableToolCall` 的 `tc.CallID` 是空的**。首版据此造出的调用 id 与结果 id 对不上,TUI 一直显示 "Waiting for tool response...",且 Modified Files 记不到。当时我把这行当成"pyte 读屏残影"放过了——**读屏里异常就先当真 bug 查**,用 HTTP 把消息快照拉下来对 id(`scripts/crushproto/query_state.py`)。现中间件 CallID 为空时取 `compose.GetToolCallID(ctx)`,再退到自增 id,单测锁定。
- trace handler 仅在 ctx 带 trace emitter(桌面端)时才挂;Crush 路径上没有,所以 usage 为 0。现独立的 `UsageObserver` + `newUsageCallbackHandler`(含流式 `OnEndWithStreamOutput`,GenerateStream 不走 `OnEnd`),仅在 ctx 有观察者时挂到 ADK 运行选项。
- `Complete` 的 token 取"最近一次调用"(Crush 的上下文用量语义),不累加。
- Windows PowerShell 5.1 解析该协议的 JSON 会出错(`Invoke-RestMethod`),查状态用 Python;`Start-Process -ArgumentList` 不会给含空格参数加引号。

**侧栏数据来源(已做)**:`crushproto` 存每会话文件版本(首次改动同时存 v0=改前)、发 `file/created` 事件、实现 `GET …/history`。改前后内容来自:`code_edit/multiedit/write` 执行前后读目标文件;`execute_bash` 前后做有界工作区扫描(≤4000 文件、单文件≤64KB、总≤16MB,跳过 `.git/node_modules/…`,二进制与未捕获改前内容的文件不报);引擎自带的 `file_change` 事件亦接入。

**5.3.2 持久化与共享工作区(已做)**
- `crushproto.Store` / `FileStore`(目录 `<DataDir>/crushproto`,按 sha256(小写规范化项目路径) 分文件):会话、消息、文件版本、token、Crush↔AgentGo 会话 link,防抖(原子写 JSON 快照);删除工作区时 flush;启动时未完成的消息 `closeUnfinished`。真实两进程重启验证:会话、消息、Modified Files、token、link 都能恢复。
- **同一项目路径只有一个 workspace 对象(引用计数)**:原先每个 `POST /workspaces` 各建一份,状态不共享,且恢复与防抖写盘互相竞态。TUI/`crush run` 退出会 `DELETE`,引用归零才清理,所以"事后查状态"只能在 TUI 存活时,或靠重启恢复。
- 门控 E2E 用 `XDG_DATA_HOME` 指临时目录,别污染真实数据。

**5.3.3 发现:AgentGo 本来没有跨轮对话历史(2026-10-03)**
- `adk_run` 之前是 `runner.Query(ctx, userText)`,每轮只有一条 user 消息;连续性全靠记忆系统(要模型自己调 `remember/recall`)。实测 PINEAPPLE:同进程第二轮答"不知道"。桌面端也是同样语义,不是 crushproto 引入的回归。
- 新增**按运行绑定、默认关闭**的 `agent.WithHistory(ctx, msgs)`,`runMessages` 把历史放在本轮输入之前;未设置时与旧行为完全一致(单测锁定)。Crush 路径由 `sessionHistory` 从 AgentGo 会话取最近消息:只重放 user/assistant 的纯文本(不含 approval/question/tool 行),最多 24 条 / 24000 rune,取最新、旧→新、必须以 user 开头(`buildHistory`)。
- **验证要用"记忆工具不会存"的探针**:`remember a secret word` 会被模型存进语义记忆,重启后也答对,分不清是历史还是记忆。有效探针:让它原样复述一句话,下一轮问"上一句有几个词"(答 5,正确)。
- 桌面端是否也默认开启历史,是产品决策,未改。

**已知缺口(下一步)**
- 审批恢复后的续跑无流式、无工具事件(`ResolveApproval` 同步、用 `context.Background()`,TUI 取消打不断它)。
- `execute_bash` 的目录扫描在并行工具下归因不确定;旧 workspace 的防抖定时器在删除/重建竞态下可能覆盖新状态(罕见)。
- 脚本:`scripts/crushproto/ask.py <host> <path> <prompt>`(无会话时自建)、`query_state.py [path]`、`drive_tui.py`。

## 5.4 深度消化 Crush:LSP 子系统全链路贯通(2026-10-03)

**背景**: 原版 Crush TUI 拥有强大的侧边栏与落地页资源监控面板。此前 AgentGo 的 `crushproto` 对 `/v1/workspaces/{id}/lsps` 接口只返回了空桩，侧边栏与落地页的 LSPs 一直显示为 `None`。

**白盒解构与方案**
1. **源码剖析**: 深入阅读 `crush/internal/lsp` 与 `crush/internal/workspace`，发现 Crush 的 LSP 核心是基于 `powernap` 的 `Manager` 与 `Client`。当通过 `Manager.Start(ctx, path)` 传入带有扩展名或根标识（如 `go.mod`）的路径时，它会启动语言服务器（如 `gopls`），并通过 JSON-RPC 收集诊断。
2. **零泄漏导出 (`pkg/codetools`)**: Go 语言禁止包外导入 `internal/`。因此在 `crush/pkg/codetools` 中为 `Toolbox` 增加对外接口与数据模型（`LSPClientInfo`, `LSPEvent`），提供 `LSPStates()`, `LSPDiagnostics(name)`, `StartLSP(ctx, path)`, `SetLSPEventListener(fn)`。
3. **工作区自动预热 (`primeLSPs`)**: 在 `Toolbox` 初始化时，异步扫描工作区根目录下的常用标记（`go.mod`, `Cargo.toml`, `package.json`, `pyproject.toml` 等）并主动触发 `Start`。
4. **crushproto 协议全接通**:
   - `GET /v1/workspaces/{id}/lsps` -> 调用 `LSPProvider.LSPStates`，返回各语言服务器状态与错误计数；
   - `GET /v1/workspaces/{id}/lsps/{lsp}/diagnostics` -> 调用 `LSPProvider.LSPDiagnostics`，返回精确到行列号与严重级别的诊断列表；
   - `POST /v1/workspaces/{id}/lsps/start` -> 接收 TUI 发送的文件路径，触发对应语言服务器启动；
   - `LSPEvent` 接入 SSE -> 当诊断发生变化或服务就绪时，向 SSE 事件流广播 `type: lsp_event, payload: {type: updated, payload: {type: state_changed | diagnostics_changed, ...}}`。

**实机验证结果**
- **单元测试**: `crush/pkg/codetools/codetools_test.go` (`TestToolboxLSPStatesAndEvents`)、`agentgo/internal/crushproto/lsp_test.go` (`TestLSPRoutesAndSSEEvents`) 均秒级 PASS。
- **实测真 TUI 读屏**: 
  - 启动后进入 Landing 引导页：自动探测到 `go.mod` 与编译错误，**面板实时渲染 `● gopls E1` (绿点+1个错误)**；
  - 驱动 AgentGo Eino 运行 `Fix the typo in main.go`：模型通过 `code_edit` 修复代码后，诊断系统自动刷新；
  - 修复后 TUI 侧边栏实时变为 **`● gopls` (0 错误，全绿)**，`Modified Files: main.go +1 -1`，顶栏显示 `◇ gpt-6-luna via Azure OpenAI in 14s`。

## 5.5 深度消化 Crush:思维链 (Reasoning / Thinking) 全链路流式渲染 (P1.9 ✅ 2026-10-03)

**背景**: 原版 Crush TUI 对带思考过程的模型（DeepSeek-R1、o3、Claude 3.7 Thinking）提供了极具仪式感的三态折叠呼吸框、耗时统计与展开交互。此前 `crushproto` 缺失该 Part 的流式组装，思考过程被丢弃或混杂在正文中。

**方案与落地**
1. **协议解构**: 查明 `crush/internal/proto/message.go` 中的 `ReasoningContent`（`thinking`, `signature`, `started_at`, `finished_at`），必须包含精确 Unix 时间戳供 TUI 计算思考耗时；
2. **Eino 端思维捕获**:
   - 在 `internal/agent` 引入 `ReasoningObserver` 与上下文绑定工具 `WithReasoningObserver`；
   - 在 `adk_run.go` 的 `drainADKEvents` 中，同时嗅探 `chunk.ReasoningContent` 以及 `msg.Extra["reasoning-content"]`、`msg.Extra["_eino_deepseek_reasoning_content"]`，向观察者派发思维增量；
   - 在 `engine_eino.go` 中，将 `ReasoningObserver` 映射为 `engine.EventReasoning`。
3. **Crush 协议端双轨聚合器**:
   - `crushproto/emitter.go`: `AssistantMsg` 扩展 `AppendReasoning(delta string)`、`ReasoningStart()` 与 `ReasoningDone()`；
   - `bridge/crushproto_turn.go`: 引入 `thinkingBuf` 与 60ms 防抖合并器；
   - **双轨无缝适配**: 既支持 `engine.EventReasoning`，也支持对普通正文流中 `<think>...</think>` 标签的流式状态机拆分；
   - 在正文 Token 到达、工具调用发起或 Turn 结束时，自动优雅闭合思维链（`ReasoningDone()` 盖上 `finished_at`）。
4. **单测验证**:
   - `TestCrushTurnReasoningEvent`: 验证思维增量聚合成 `reasoning` Part，正文聚合成 `text` Part，耗时戳完整闭合；
   - `TestCrushTurnThinkTags`: 验证 `<think>` 标签流自动剥离并升格为原版 Crush 思维块。

## 5.6 深度消化 Crush:动态模型配置与实时热切换 (P1.10 ✅ 2026-10-03)

**背景**: 此前 `/v1/workspaces/{id}/config`、`/providers` 与 `/agent` 均为静态 fixture，TUI 选模型弹窗显示写死的模型，按 `ctrl+l` 切换无响应，底层 AgentGo 依然保持原配置。

**方案与落地**
1. **ModelProvider 解耦接口 (`internal/crushproto/server.go`)**:
   - 抽象 `ModelInfo` 与 `ModelProvider` 接口（`CurrentModel`, `SetModel`, `AvailableProviders`）；
   - 在 `Server` 初始化与各核心路由全面替换写死 fixture。
2. **动态路由贯通 (`internal/crushproto/routes.go`)**:
   - `GET /v1/config` & `GET /v1/workspaces/{id}/config`: 动态注入当前活跃的 `large` / `small` 模型与 Provider；
   - `GET /v1/workspaces/{id}/agent`: 动态返回活跃模型元数据与状态；
   - `GET /v1/workspaces/{id}/providers`: 动态聚合模型清单并确保当前模型列为 Provider 首选；
   - `POST /v1/workspaces/{id}/config/model`: 解析 `ConfigModelRequest`，下发给 `ModelProvider.SetModel`，并向 SSE 广播 `config_changed` 事件，驱动所有挂载的前端实时刷新；
   - `POST /v1/workspaces/{id}/agent/update`: 广播 `config_changed` 事件，支持 TUI 热加载。
3. **AgentGo 运行时桥接 (`internal/bridge/crushproto_model.go`)**:
   - `crushprotoModelBridge` 与 `rt.LLMConfig()` / `rt.SetLLMConfig()` 打通;根据 `api_base` 推断 provider id(azure/openai/ollama/anthropic/…);
   - **只暴露 AgentGo 实际配置的 provider,模型为主模型 + fallback**;`SetModel` 校验 provider/模型,非法返回 400,选 fallback 时与主模型互换;
   - 在 `crushproto_plugin.go` 注册。
4. **配置覆盖 `overlayModels`**:TUI 的选择器读 `GET /workspaces/{id}` 体内**内嵌的 config**,不是只读 `/config`;因此 `/v1/config`、`/workspaces/{id}/config`、工作区体内嵌 config 三处共用同一个覆盖函数。
5. **验证**:
   - 单测 `TestModelDynamicConfigAndSwitching`(含内嵌 config 断言)、`TestModelBridgeOffersOnlyConfiguredModels`、`TestModelBridgeSwitchSwapsFallbackAndRejectsForeign`;
   - **真 TUI 实机**(headless + 真 `crush.exe`,ollama 配置 qwen3/llama3):顶栏 `qwen3 via AgentGo (ollama)`;`ctrl+l` 首组只含两个模型;选 `llama3` 出现 `Large model changed to llama3`,`config.json` 落盘 `model=llama3 / fallback_model=qwen3`,重开选择器当前项为 `llama3`;
   - **局限**:选择器下半部分的 Bedrock 等他家 provider 来自 TUI 本地 Catwalk 清单,服务端无法隐藏;误选会弹 API key 框,切换请求则被服务端 400 拒绝。
   - reasoning:用 `scripts/crushproto/fake_llm.py`(流式 `reasoning_content` / `<think>` 标签)驱动真实 Eino 路径 + 真 TUI,`Thinking 1s` → 思考文本 → 正文均正常,`reasoning` part 的 `started_at`/`finished_at` 已写入;`<think>` 标签可被切在 chunk 边界(单测覆盖)。真实推理模型与折叠三态交互**尚未实测**。
6. **并发教训**:`listWorkspaces` 持 `Server.mu` 时调 `workspaceBody`(内部经 `activeModel` 再取 `Server.mu`)会自死锁;已改为持锁拷贝、解锁构造,并有回归测试 `TestListWorkspacesDoesNotDeadlock`。
## 6. 风险与对策

- **范围蔓延**:P1 只做"注册点返回 dispose + 走服务表",不搬业务代码;一方插件迁移放 P4 逐个做。
- **进程内插件无法热卸载 Go 代码**:热重载只重载"注册关系"和 sidecar,不换 Go 二进制;这对一方插件足够。
- **sidecar 孤儿进程**:P2 同时用 Job Object(Windows)收口,`Scope` 里挂 kill。
- **依赖环**:启动时拓扑排序即报错,不进入运行态。
- **治理旁路**:所有贡献必须经 `Contribute`,P1 起在 `arch` 测试里禁止直接写 Router/Registry 的包外调用(白名单:plugin 宿主)。
