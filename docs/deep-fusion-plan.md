# AgentGo 深度融合实施计划

日期：2026-10-04。状态：F0、F1、F5 已完成；F2、F3、F4、F6 部分完成（见 §6 状态表）。

**范围承诺**：只承诺 F0 + F1，二者已交付。F2–F6 按 §6 记录重新评估范围与顺序，不预先锁定（F3 会话迁移、F5 装配权移交各自相当于一次大重构）。

目标：Crush TUI、OpenSumi GUI、无头入口共享任务状态、执行语义与能力服务；编码、模型、记忆、审批、工作流逐步成为可替换插件。

本计划是 `plugin-architecture-plan.md` 的实施补充。旧计划已完成的 Host、Router、codetools、Crush 协议工作继续保留；本轮优先收敛运行语义，再推进旧计划中的市场、manifest 和面板扩展。阶段编号使用 F0–F6，避免与旧 P0–P5 混淆。

## 1. 已核查的起点

- `internal/plugin` 已提供服务注入、依赖排序、Scope、装卸和事件中间件。
- Crush TUI 已通过 `crushproto → engine.Router → Eino` 调用 AgentGo。
- codetools 已贡献编码工具；工具适配、可见性和治理仍有多个注册入口。
- `bridge.NewRuntime` 仍直接创建主要子系统，之后 `AppService.initPlugins` 才启动插件宿主。
- 插件捕获 `AppService`，真实依赖多于 `Inject` 声明。
- Crush 协议使用独立 FileStore，并将会话映射到 AgentGo session。
- 审批续跑包含界面适配层逻辑；codetools 绑定加载时的工作区。
- 插件事件中间件尚未充分接入生产执行主链。

以上是源码审计结论；历史文档中的测试通过记录不代表本轮重新验收。

## 2. 设计约束

1. 最小宿主负责依赖装配、Scope、扩展点和生命周期。默认 agent loop 作为一方插件提供。
2. 使用现有 Go 静态插件机制；需要进程隔离的第三方能力再使用 sidecar。
3. 每项状态只有一个权威来源。协议缓存是可重建的视图，不能成为第二份业务事实。
4. 插件通过明确服务接口协作。组合入口可装配具体实现，插件实现不捕获整个 AppService。
5. GUI/TUI 负责提交命令、消费事件。审批、取消和续跑由统一执行服务负责。
6. 工作区、会话、运行分别有明确作用域；运行中的工具固定绑定本次运行的工作区。
7. 保留当前未提交工作。逐阶段迁移，不重置工作树，不同时重写全部子系统。
8. 每阶段完成针对性验证后才能进入下一阶段；跳过项必须写明原因。

## 3. 服务契约与所有权

| 服务 | 权威职责 | 首个迁移对象 |
|---|---|---|
| WorkspaceService | 工作区身份、代次、变更通知、运行绑定 | codetools 与普通工作区工具 |
| SessionService | 会话、持久化事件、历史投影、协议 ID 映射 | Crush FileStore 与 AgentGo sessions |
| RunService | 开始、暂停、恢复、取消、终态、运行事件 | Router 与审批续跑 |
| ApprovalService | 请求、决定、有效期、幂等、审批策略 | AgentGo 审批队列与 Crush 权限弹窗 |
| ModelService | 模型设置、可用能力、设置变更 | Eino 与 Crush 模型桥 |
| ToolService | 注册、发现、可见性、策略与执行 | Registry 与 codetools |

先提取本阶段需要的最小接口，不一次性制造空接口。`internal/plugin` 保持不依赖具体业务插件；业务契约放在独立包或现有对应领域包，最终位置在 F0 核查后决定。

## 4. 阶段与验收

### F0：建立基线和变更边界

- [x] 建立回退点。仓库只有一个 `init` 提交，工作树约 260 个未提交改动（40 修改 / 35 删除 / 185 未跟踪），不替用户提交；源码快照备份到 `D:\dev\backup\agentgo-f0-baseline`（排除 node_modules、dist、.git、exe）。
- [x] 记录基线：`go build ./...`、`go vet ./...`、`go test ./...` 均退出码 0（34 个包 ok、0 FAIL、19 个包无测试文件）。**没有历史失败**，之后任何失败都是新增的。输出在 `D:\dev\tmp\f0`。
- [x] 核查 `plugin` 与 `compose/*` 的重叠职责，指定唯一生命周期装配入口。
- [x] 画清读取、修改、审批、恢复、结果保存的实际调用链（审批部分见下）。
- [x] 核查桌面端、嵌入 TUI、无头进程的数据路径和跨进程运行方式（见下）。
- [x] 补治理续跑回归测试（F2/F4 的前置）：`internal/governance/adk_resume_test.go`，用生产同款 `ComposeToolMiddleware` + `ChatModelAgent` + 内存 checkpoint，脚本化模型发出需审批的工具调用 → 中断 → 新 runner `ResumeWithParams`，覆盖批准/拒绝 × 值/指针 payload，断言工具次数与最终回答。变异验证：去掉指针兜底后恰好指针用例失败，症状与线上一致。背景：2026-10-03 因 gob 未注册、指针/值类型断言各出过一次"静默失败"，见 `p2-embedded-terminal.md` §4.3。

验收：基线与范围记录可复查；明确首批文件、依赖及现有测试。无需真实模型调用。

**F0 记录（已查明的事实）**

- **唯一装配入口 = `internal/plugin.Host`**。生产代码只有 `bridge/{app_service,codetools_plugin,crushproto_plugin,engine_crush,plugins,plugins_ipc}.go` 引用它。`internal/compose/{lifecycle,reconciler,service}` 在 `compose` 之外**零引用**（`service` 仅被自己的测试引用），是死代码。决定：不再往 `compose/*` 添加任何东西；是否删除另行决定（不属于本计划）。
- **工作区切换缺口（F1 目标）**：`AppService.SetWorkspaceRoot → Runtime.SetWorkspaceRoot` 已更新 wsMiddleware、skillLoader、工作区绑定工具、治理策略、`agentRunner`，**唯独不重建 codetools**。`codetools_plugin.go` 自己的注释也写着"切换后需 reload 插件"。其 `Apply` 通过 `s.rt.WorkspaceRoot()` 读取根目录，并用 `s.setCodetools` 写回整个 AppService，这是对 AppService 的两处隐式依赖。
- **审批的三个决策点**：(a) Eino 治理中间件 `governance.invoke_policy`（风险分级 → `StatefulInterrupt` → `ApprovalQueue`）；(b) codetools 原生 `Approver`：AgentGo 默认 `nil`，语义是"全部放行不询问"，仅 `AGENTGO_CODETOOLS_APPROVE=0` 时全拒绝，所以现状下 codetools 的变更类工具是否需要审批完全取决于 (a) 是否把它们归为需审批——**F2 要先验证这一点，不能假设**；(c) `crushRunner.approve → AppService.ResolveApproval → Runner.ContinueAfterApproval → ResumeInterrupt`，续跑业务逻辑目前在 bridge 里。
- **执行入口选型（F2 前提）**：以 (a) Eino 治理中间件为唯一决策来源，(b) 保持"放行"，由 (a) 在其之前决定；(c) 的续跑逻辑在 F4 迁入执行服务。依据：(a) 是唯一带持久化队列、审计与 checkpoint 的路径，GUI/TUI/无头都经过它。
- **数据路径与进程模型**：`cmd/agentgo`（桌面）、`cmd/agentgo-headless`、`cmd/agentgo-tui` 都调用 `bridge.NewRuntime()`，数据目录同为 `xdg.DataFile("agentgo")` 的上级目录，共享 `agentgo.db`、`config.json`（含工作区根）。**每个进程各有自己的 Runtime**：内存里的 `pendingStore`、运行控制、插件宿主、codetools 工具箱互不可见。因此"共享数据库目录"≠"共享活跃运行"，TUI 发起的审批桌面端的内存里看不到对应的续跑上下文（F6 的前提问题，F0 记录在案，不在 F1 解决）。
- **审批入口清单**：`AppService.ListPendingApprovals/ResolveApproval`（GUI 与 Crush 共用）、`crushRunner.approve`（Crush）、`features_ipc.go:98`（ask_user 续跑）、`workflow_interrupt.go`（工作流续跑）。

### F1：先统一工作区和编码插件生命周期

- [x] 为 codetools 提供显式 workspace、data-path 与 toolbox 发布依赖：`codetoolsEnv{WorkspaceRoot, DataDir, Publish}`。
- [x] 消除 codetools 实现对整个 AppService 的捕获：`codetoolsPlugin(env)` 不再接收 `*AppService`；仅 `AppService.codetoolsEnv()` 这一个组合点接触 Runtime。
- [x] 工作区切换触发受控重绑定：`Runtime.AddWorkspaceBinder`，`AppService.rebindCodetools` 重载插件，工具注册、可见性（static tool）、grant、LSP 随插件 Scope 一起释放并重建。钩子放在 `Runtime.SetWorkspaceRoot` 内而不是 `AppService.SetWorkspaceRoot`，因此 GUI、ACP（`acp_handler.go` 直接调 Runtime）、测试等所有入口自动受控。
- [x] 事务边界：先应用 Runtime 状态，再跑绑定器；任一绑定器失败 → 恢复上一个工作区（`applyWorkspaceRoot(prev)`）并重绑，返回带原因的错误。无上一个工作区时无法回退，直接报错。
- [x] 占用时拒绝切换：`ErrWorkspaceBusy`。占用 = `Runner.ActiveRuns()>0`（进行中的运行）或 `pending.Len()>0`（等待审批、之后会续跑的运行）。同一路径的"切换"是空操作，不重载。用户手动停止的 codetools 切换后保持停止。
- [x] `AppService.Close` 注销自己的绑定器，已关闭的服务不会被后续切换重载。

验收：A→B 后所有编码工具操作 B；失败切换保留 A；停止/重载无重复注册；旧 LSP 与资源退出。最小 Runtime 测试装配继续可用。

**F1 完成记录（2026-10-04）**

- 改动：`bridge/{workspace_runtime,codetools_plugin,plugins,app_service,engine_crush,pending,runtime}.go`、`agent/{runner,run_control}.go`；新增测试 `bridge/codetools_rebind_test.go`、`governance/adk_resume_test.go`。
- 新增测试（6 + 4 子用例）：A→B→A→B 切换后用相对路径经 `code_view` 读取，内容分别来自当前工作区且各工具注册恰好 1 份；绑定器否决 → 回到 A、工具仍读 A、插件 running；待审批时拒绝且 `AppService.SetWorkspaceRoot` 返回 `success:false`，处理后可切；同路径空操作；手动停止保持停止；Close 后注销。
- 变异验证：去掉绑定器注册 → 切换测试失败，症状为"tools still bound to the old workspace"，即原缺陷。
- 验证：`go build ./...`、`go vet ./...`、`go test ./...` 全部通过（含 `internal/arch` 架构约束）；`go test -race` 对 governance、plugin、bridge 相关用例通过。未构建 TUI/headless 二进制与前端：本阶段没改它们的入口，`go build ./...` 已覆盖编译。
- 跳过/未验证：旧 LSP 进程退出只靠既有 `Scope.Effect → tb.Close()` 机制保证，没有单独断言 gopls 进程消失（测试环境不保证有 gopls）。
- 遗留风险：(1) 待审批请求如果一直不处理，会一直阻止切换工作区——这是有意的保守策略，但 UI 需要把 `error` 文案展示出来，前端尚未核对；(2) 每个进程各有一份 `pending`，别的进程（例如 TUI）里挂起的审批桌面端看不见，不会阻止桌面端切换（F6 问题）；(3) 切换期间有一个很短的窗口 code 工具不可用（重载过程中），此时进来的调用会得到"工具不存在"，因为占用检查已排除进行中的运行，这个窗口只影响新发起的调用。
- 回退：`D:\dev\backup\agentgo-f0-baseline` 为 F0 时的源码快照；F1 的改动集中在上面列出的文件。

### F2：让插件进入真实工具执行链

- [x] 选定一个权威执行入口，把现有 Eino 治理包装接到该入口。
- [x] 接入有类型的 pre-execute / post-execute 扩展点（execute 环绕点有意未做，见完成记录），携带 run、session、workspace、call ID 和取消上下文。
- [x] 明确排序、拦截、失败、取消和结果语义；非幂等工具不做隐式重试。
- [~] 工具声明统一携带来源与修改属性（风险等级仍按名字，未迁移），减少按工具名称维护的分散规则。
- [x] codetools 原生 Approver 与统一治理协作：策略只有一个决策来源，避免重复弹窗或默认放行绕过。
- [x] agent 失败后回退纯聊天必须显式报告能力降级；编码任务不能伪装成成功执行。

验收：拒绝和取消都没有副作用；批准只执行一次；结果与原 call ID 对应；插件能拦截真实调用，卸载后拦截消失；GUI/TUI/无头遵守同一策略。

**F2 前置核查（2026-10-04，已完成，见下）**

- 实测发现并修复一个放行漏洞：`code_edit`、`code_multiedit`、`code_write` 在 balanced 与 strict 策略下都**不经审批直接执行**。原因：风险表给了 `RiskMedium`，而 `Policy.requiresApproval` 只拦 High/Critical；`create_workspace_file` 等靠被列入 `workspaceMutationTools`（balanced/strict 据此提升为 High）才被拦，codetools 的写入工具漏了这一步；codetools 自带 `Approver` 在 AgentGo 里为 nil（全放行），所以实际没有任何闸门。
- 决定（用户确认）：balanced 与 strict 都要审批，`open` 模式保持不拦（既有语义）。最初的修法是把 `code_*` 写进 `workspaceMutationTools` 名单，随后被下面的"工具特性登记"取代（名单只保留内置的两个工具）。**行为变化**：balanced 下自主修复循环中每次改文件都会请求批准，headless/TUI 需用 `-yolo` 自动授权。
- 新增 `codetools/policy_gate_test.go`：用生产同款 `BuildPolicy` + 治理中间件，对工具箱**实际提供**的每个写入类工具断言必须被拦；只读工具不能被拦；工具集发生变化（新增 Crush 工具）时测试失败，强制显式归类。变异前该测试对 3 个工具 × 2 策略共 6 处失败。
- 工具箱实际只有 12 个工具，没有 `lsp_rename`/`lsp_replace_symbol`；风险表和 `IsMutating` 里的这两项是预留项。
- 原先分散的"写入"分类：`codetools.IsMutating`、`governance` 的 `workspaceMutationTools` 与风险表、`tools.RegisterStaticTool(…, mutating)`。

**F2 完成记录（2026-10-04，进行中的 F2 的已交付部分）**

已交付：
- **权威执行入口 = `governance.GovernanceMiddleware.InvokeWithPolicy`**。ADK 的 `ChatModelAgent` 经 `ComposeToolMiddleware` 走它，GUI、Crush TUI、headless、ACP 都是同一个 `buildChatModelAgent`，因此同一套策略。已核对 `RequiresApprovalFor` 是唯一的审批决策点，无旁路。
- **工具特性登记**（`governance/tool_traits.go`）：插件为自己贡献的工具声明 `ToolTraits{Source, Mutating}`，策略在**决策时**动态查询：非 open 模式下声明会改文件的工具必须审批，且风险不低于 Medium（否则会落入中间件的"低风险直通"）。codetools 插件随工具登记、随插件卸载撤销（`codetools.RegisterTraits`，`c.Effect`）；重载时旧撤销函数不会抹掉新登记。codetools 的"会改文件"现在只有 `IsMutating` 一个来源，并同时驱动审批与只读模式可见性（`RegisterStaticTool`）。
- **工具 pre/post 扩展点**（`governance/tool_hooks.go`）：`RegisterToolHook(ToolHook{Name, Before, After})`，`ToolCall` 携带 CallID、工具名、实际参数、会话、工作区。语义已固定并有测试：按注册顺序；只包住"真正执行"，等待审批、被拒绝、被策略拒绝的调用不会触发；`Before` 返回错误即否决（工具不执行，模型收到带 hook 名的 rejected 结果，后续 hook 与 `After` 跳过），`Before` panic 同样否决（fail closed）；`After` 只观察、panic 被吞并记日志；`ctx` 已取消的调用不执行；治理从不重试。注销后拦截立即消失。批准后续跑与直接执行走同一包装。顺带修了 `ComposeToolMiddleware` 丢失 `CallID` 的问题（向下传递时没带）。
- **降级显式化**（`bridge/message.go`）：agent 运行失败退回纯聊天时，回复开头带"[能力降级] 智能体运行失败（原因）……不代表任务已完成；失败前的步骤可能已部分执行"，`SendMessageResult.Degraded` 携带原因；终端快速修复识别 `Degraded`，不再把失败提示当成命令解释。
- 测试：`governance/{tool_traits,tool_hooks}_test.go`、`codetools/policy_gate_test.go`、`bridge/message_degraded_test.go`、`bridge/codetools_rebind_test.go` 中的 traits 生命周期用例。hook 与审批用例都经真实 `ChatModelAgent` + checkpoint 链。验证：全量 `go build/vet/test` 通过，governance/codetools/bridge 的 `-race` 通过。

未做 / 跳过：
- **没有"execute"环绕点**（可替换或包裹执行本身）。当前只有 Before 否决和 After 观察。原因：没有真实需求，替换执行会打破审批与审计的假设，等出现具体插件再设计。
- **风险等级仍在 `defaultRiskTable` 按名字维护**，`ToolTraits` 只承载"来源"和"是否改文件"。把风险等级也迁入声明，需要先决定内置工具（几十个）怎么声明，留到有明确收益时做。
- **没有真实插件使用 hook**：只在测试里验证。第一个使用者出现时应通过 `c.Effect` 注册 `RegisterToolHook` 的 dispose，从而随插件卸载。
- 降级路径只做了纯函数测试，没有在真实 agent 失败场景下端到端复测（构造 Runner 过重）。
- 降级回复仍会写入记忆（沿用原行为）；是否应该跳过，需要另行决定。
- 前端未适配 `Degraded` 字段（可选字段，旧前端忽略，仍能看到文字提示）。

### F3：统一会话事实与事件投影

- [ ] 为现有 sessions 增量增加持久化运行事实：输入、工具调用与结果、审批、终态、用量及必要上下文。
- [ ] 定义事件 ID、序号、提交点与重连语义。瞬时 token 可独立流式传输，最终消息先持久化再发布提交事件。
- [ ] Crush parts、权限与 session 结构由统一日志投影生成；Crush ID 映射留在适配边界。
- [ ] 为旧 FileStore 做带备份、可重入且可回退的导入；导入验证前保留原数据。
- [ ] 明确日志展示与执行恢复的区别；恢复仍依赖 checkpoint、工具幂等性和外部资源状态。

验收：重启和重连后消息不丢失、不重复；相同会话在两个界面的最终消息、审批状态和工具结果一致；旧记录可迁移，损坏导入不覆盖有效数据。

**F3 范围评估（2026-10-04，实现前的核查）**

- 现状有两份"会话事实"：`sessions.Store`（SQLite；AgentGo 的权威文字转录，含审批行、aui 行；Crush 的轮次也经 `runEngineTurn` 写进来，所以 TUI 与桌面共享同一个会话列表和模型历史）和 `crushproto.FileStore`（Crush 的富视图：parts、files、用量、标题，经 `Links` 映射到 AgentGo 会话 ID，400ms 防抖落盘）。
- **缺口不在"两份转录"，在"运行事实"**：工具调用/结果、文件变更、审批请求、终态只存在于瞬时事件流或 Crush 的 FileStore；`sessions.Store` 只有最终文字，且在运行结束后才写。进程中途崩溃时只剩用户消息，没有任何终态标记可判断"这次运行怎么了"。
- 引擎已有统一的、带序号的类型化事件流，Router 保证每个运行恰好一个 `done`。所以 F3 不需要另造事件模型，只需持久化其中的"事实子集"。
- 因此 F3 拆成可独立交付的切片：**F3a 运行事实日志（本次已做）**；F3b 把 Crush 的 parts/权限视图改为从日志投影、替换 FileStore，并做带备份的导入——这是计划里风险最高的部分，且在没有第二个界面真正读取同一会话（F6）之前收益不明确，**推迟**；F3c 桌面端 `stream.go` 旧路径接入同一日志（见下，未做）。

**F3a 完成记录（2026-10-04）**

- 新表 `chat_run_events(session_id, seq, run_id, kind, call_id, name, payload_json, created_at)`，主键 `(session_id, seq)`，纯增量（`CREATE TABLE IF NOT EXISTS`，不改既有表）。`seq` 在单条 `INSERT … SELECT MAX(seq)+1 … RETURNING` 里分配，并发追加不会撞号（40 路并发测试无间隙无重复）；会话删除时一并清理。
- 记录的事实：`status`、`tool_call`、`tool_result`、`file_change`（只存 unified diff，不存前后全文）、`approval_request`、`error`；每个字符串字段截断到 16KB（按 UTF-8 边界）。**token/reasoning 增量不入库**（瞬时）。
- **提交点**：`done` 事件在运行的最终消息写入 `chat_messages` **之后**才追加（`runEngineTurn` → `commitRun`），所以日志以 `done` 结尾意味着转录完整；没有 `done` 即"未完成"。`LastRunState` 从日志推导状态：`unfinished`（崩溃/仍在运行）/ `awaiting_approval` / `completed` / `failed` / `none`，不单独存状态。
- **重连语义**：`AppService.GetRunEvents(sessionID, afterSeq, limit)` 返回 `seq > afterSeq` 的事件和当前运行状态；客户端记住最后的 `seq` 即可续读。
- 覆盖入口：`runEngineTurn`，即 Crush TUI、`StartEngineRun`、`RunEngineSync` 都经过它。
- **顺带修复的既有缺陷**：`GetMessages` 原为 `ORDER BY created_at ASC LIMIT n`，取的是**最老**的 n 条。`sessionHistory` 用 `GetMessages(…, 500)` 给模型喂历史，会话超过 500 行后新对话永远进不了上下文；桌面端历史 200 条也是最老的 200 条。现改为取最新 n 条再按时间升序，并用 `rowid` 打破同一秒内的并列（`created_at` 只有秒级精度）。有回归测试。
- 测试：`sessions/run_events_test.go`（编号、续读、并发、状态推导、删除、最新 n 条）、`bridge/run_log_test.go`（假引擎经真实 `runEngineTurn`：事实子集与顺序、提交点在消息之后、暂停/失败终态、超大输出截断、无会话为空操作、UTF-8 安全截断、`GetRunEvents`）。全量 `go vet/test` 通过，sessions/bridge 的 `-race` 通过。

未做 / 遗留：
- **桌面端旧路径 `stream.go` 与 `sendMessageCore` 直调（`SendMessage`、ACP、终端快速修复）不写运行日志**：它们不经过 `runEngineTurn`，也不经过 Router，没有 `run_id`。需要先确认前端还在用哪条路径再决定是迁到 `runEngineTurn` 还是单独接入。
- **审批续跑（`ResolveApproval`）产生的工具调用/结果没有进日志**：续跑发生在 Router 之外，目前日志里只会看到 `done{pending:true}`，续跑结果没有对应的 `done`。这是 F4 要收回的控制权的一部分。
- 崩溃后的 `unfinished` 目前只是可被读出，没有启动时的自动标记或清理。
- 前端没有消费 `GetRunEvents`；`FileStore` 与日志仍是各自独立的两份，没有对账。
- 迁移（F3b）完全没做，所以"旧记录可迁移、损坏导入不覆盖有效数据"这条验收**未满足**，F3 整体不勾选。

### F4：收回审批和续跑控制权

- [ ] RunService 统一处理 running / awaiting_approval / completed / failed / cancelled 转换。
- [ ] 保留 Router 当前回合 EventDone 契约，区分回合结束与整个任务终态，避免审批期间误报任务完成。
- [ ] 将 `crushRunner.approve` 中的业务续跑移到执行服务；适配层仅转换请求与用户决定。
- [ ] 重复决定幂等；过期或已取消运行不恢复；多次连续审批仍保持完整事件流。
- [ ] 崩溃恢复时校验 checkpoint 和待决请求；缺失条件明确失败，不盲目重做文件修改。

验收：连续两次审批、拒绝、等待时取消、批准时取消、重复提交和重启恢复均有确定结果；每个工具调用最多一次实际执行，任务终态只提交一次。

**F4 开工前的核查（2026-10-04）**

续跑逻辑有两份：桌面 `ResolveApproval` 自己做续跑，Crush 的 `crushRunner.approve` 再套一层调用它。核查发现的实际缺陷（比计划里的"收回控制权"更具体）：

1. **连续审批会丢。** `ResolveApproval` 续跑后只取 `runRes.Content`，完全不看 `runRes.PendingApproval`。续跑途中遇到第二个需审批的工具时，没有任何人登记待决请求：审批卡能在队列里出现，但点批准后因找不到待决运行而什么都不发生，却返回成功。
2. **"批准但没有执行"报成功。** 待决运行只在内存（`pendingStore`），进程重启后丢失；此时批准会把审批行改成 approved，然后静默不做任何事。`InterruptID` 为空或运行器不可用时同样静默。
3. **续跑不可取消。** `ResolveApproval` 用 `context.Background()`，Crush 里取消运行传不到续跑。
4. **读后删除不是原子的。** `Get` … 续跑 … `Delete`；靠队列行 `status='pending'` 才没有重复执行，没有第二道保险。
5. **续跑不进运行日志**，被暂停的运行在日志里永远是 `awaiting_approval`。拒绝同样如此。
6. **Crush 适配层先报"Approved and executed."再检查续跑是否出错**，且忽略决定记录失败。

**F4 完成记录（2026-10-04，部分）**

新增 `bridge/approval_resume.go`，`resolveApproval(ctx, …)` 成为用户决定转为动作的唯一入口；`AppService.ResolveApproval`（Wails 绑定，签名不变）和 Crush 适配层都只做转换与展示。

- 决定先落队列（重复决定被拒绝），再用 `pendingStore.Take` **原子认领**续跑权（并发 64 路只有 1 路拿到）。
- **续跑后再次暂停与首次暂停同样登记**（共用 `registerPending`，`message.go` 首轮也改用它）：待决请求、`approval:pending` 事件、审批消息及其入库一致。矩阵路径同样登记。
- 所有"没有续跑"的情形都返回 `resume_error`：无待决运行（重启/已处理）、无运行器、无检查点 ID、续跑失败、被取消。批准不再静默成功。
- `ctx` 贯通：Crush 把 TUI 运行的 ctx 传下去，取消会取消续跑；续跑另有 `engineRunTimeout` 上限。等待时（决定记录前）取消 → 不记录决定，审批保持打开，之后仍可决定。
- **运行日志**：若日志显示该会话的运行在等待审批，续跑追加 `status: resumed`，结束后再提交一次 `done`（同一 `run_id`，可再次为 `pending`）；拒绝提交 `done{resolution:"rejected"}`。`LastRunState` 改为"以最后一条事件为准"，所以续跑中途崩溃读出 `unfinished`，而不是旧的 `awaiting_approval`。
- Crush 适配层：失败如实显示（不再先报成功）；续跑再次暂停时继续逐个询问用户（连续审批）。
- 旧测试 `TestBridgeGovernancePendingResolve` 原先断言"无运行器时不报 resume_error"，那正是缺陷 2，已改为断言必须报错。
- 测试（`approval_resume_test.go`、`pending_test.go`、`sessions` 新用例）：连续两次审批（含入库与日志序列）、重复决定、16 路并发决定、拒绝、等待时取消、续跑中取消、重启后无待决、续跑失败、无运行器、崩溃后状态。变异验证：去掉第二次登记、去掉拒绝提交，对应测试都会失败。`go vet ./...`、`go test ./...`、`-race`（sessions、bridge）通过。

**F4 第二段：重启恢复（2026-10-04）**

开工前又核出三个事实，决定了设计：

- 待决运行只在内存；但 Eino 检查点（`agent_checkpoints`）和审批队列都在 `agentgo.db`，**是持久的**——缺的只是"哪个审批对应哪个检查点"的那一行。所以重启后真能恢复，不必"明确失败了事"。
- `StartApprovalEscalation` 早就会在 30 分钟后把待决审批标为 `expired`，但**只改队列行**：内存里的待决运行永远留着，`pending.Len()>0` 会让切换工作区永远被拒绝，日志里的运行永远停在 `awaiting_approval`。
- `agentgo`、`agentgo-tui`、`agentgo-headless`、`mcphost` 各自 `NewRuntime()`、共用同一个库。启动时的对账**不能碰别的活进程的运行**。

做了什么：

- 新表 `pending_runs(approval_id, session_id, owner_pid, payload_json, created_at)`；`pendingStore` 带落库后端，`Set` 写入（记本进程 PID）、`Take`/`Delete` 删除。会话删除时一并清理。
- `RecoverPending`（`NewRuntime` 末尾执行一次，`bridge/recovery.go`）：
  - 属主进程已不存在的行才处理（`processAlive`：Windows 用 `OpenProcess`+`GetExitCodeProcess`，类 Unix 用 `kill(pid,0)`；判断不了一律当作"活着"，宁可不动）。
  - 审批仍待决且检查点存在 → **采纳**到本进程，属主改为本进程，之后照常批准即可续跑。
  - 检查点已不存在 / 无 InterruptID → 在队列里拒绝该审批（卡片不再悬空），日志运行记为失败并写明原因。
  - 审批已 `expired` → 失败；已 `rejected` → 按拒绝关闭；**已 `approved` 但进程在续跑前停了 → 失败，原因写明"工具没有被重新执行"**。恢复流程**从不执行任何工具**，这是"每个工具调用最多一次执行"跨崩溃成立的前提。
  - 日志里停在等待审批、但已没有任何待决行指向它的运行 → 关闭为"待恢复的运行已丢失"。
  - ask_user 提问本来就不进审批队列，缺队列行是正常的，按检查点校验后照常采纳。
- 到期清扫 `sweepExpiredPending`：升级任务每个 tick 都通知（共库的别的进程也会把审批标过期，不能只靠自己的计数）；只处理 `expired`，用 `Take` 认领，所以不会和正在应用的决定抢；释放后切换工作区不再被卡住。
- 顺带修复：`AnswerQuestion` 回答后运行再次暂停时同样没人登记（与连续审批同类缺陷），现在走 `registerPending`。
- 顺带修复（与本任务无关，由全量测试偶发暴露，约 1/7 概率）：`internal/agent/bgtask.go` 后台命令在 `cmd.Wait()` 后立刻关闭更新流，而读协程可能还在 `Send` → `panic: send on closed channel`，生产里会直接崩进程。现在先等读协程退出再收尾（`cmd.Wait` 已关闭管道，不会卡住）。修复后连跑 40 次无失败。
- 测试（`recovery_test.go`、`sessions/pending_runs_test.go`）：落库/释放、**重启后批准仍能续跑**、不碰活进程的运行、检查点缺失、已批准未应用不重执行、拒绝、无人指向的暂停运行、ask_user 采纳、坏行丢弃、到期释放且不误伤待决/正在应用的审批。变异验证：把"已批准未应用"改成重新采纳、把所有属主当死亡、让清扫误伤待决审批，对应测试都失败。`go vet`、`go test ./...`、`-race`（sessions/bridge/governance/agent）通过。

仍然**未做**：
- 审批队列的"批准"仍会在找不到待决运行时把审批行标成 approved 然后报 `resume_error`。采纳之后这种情况只剩"属主是另一个活进程"（例如桌面应用持有、在 TUI 里点批准）——跨进程批准属于 F6。
- 没有单独的 `unfinished` 启动收尾：日志停在非 `done` 的运行无法区分"别的进程仍在跑"，所以不自动关闭。
- 属主 PID 可能被系统复用；复用时对账会把死进程当成活的，**结果是不处理（残留）而不是误处理**。

对照清单，**未完成**的部分：
- [ ] "RunService 统一处理 running/awaiting_approval/completed/failed/cancelled"：没有做成一个服务对象。目前状态由日志推导（`unfinished/awaiting_approval/completed/failed`），**没有 `cancelled`**——取消被记为 `failed`（error 含 "canceled"）。拒绝记为 `completed + resolution:rejected`。
- [ ] "区分回合结束与整个任务终态"：Router 的 `EventDone` 未动；区分靠日志的 `done.pending`。续跑段的产出不经过 Router 事件流，只经 `resolveApproval` 的返回值和 `chat:done` 事件，Crush TUI 以外的订阅者看不到续跑的逐步事件（日志里只有 `resumed` 和 `done`，没有续跑中的 tool_call/tool_result，因为 `ContinueAfterApproval` 不是流式）。
- [x] "过期的运行不恢复"：已完成（见"第二段"：到期清扫；队列早有 30 分钟过期，原先只是没连到内存里的运行）。
- [x] "崩溃恢复校验 checkpoint 和待决请求"：已完成（见"第二段"）。
- 拒绝后不会把"被拒绝"告诉智能体继续（沿用旧行为：运行就此结束，检查点留着）。是否应让模型看到拒绝并改道，需要产品决定。

### F5：把装配权移到插件宿主

- [x] 按依赖顺序将 sessions、model、approval、tools、默认 Eino loop、memory、workflow、scheduler 迁成服务插件。
- [x] NewRuntime 缩为宿主创建和组合配置；bridge 保留传输适配与兼容入口。
- [x] 配置描述启用插件集合，提供实际依赖图、状态、失败原因和能力探测（`PluginGraph()`、`Capabilities()`）。
- [x] 核查全部后台任务、监听器、LSP、子进程的 Scope 清理：启动期的全部循环、agent 后台命令、`job_monitor` 任务均已归属并有测试；其余异步子进程来源逐一核对过归属（见"补完"一节）。
- [x] 增加针对性架构约束，阻止核心反向依赖具体插件及迁移后的隐式 AppService 依赖（`crushproto` 一项显式登记在白名单，只许缩减）。

验收：更换模型/记忆实现无需改 agent 消费者（**两者都有测试证明**）；缺少可选插件仍可启动；卸载按依赖顺序完成；关闭后相关资源释放。**全部满足。**登记在案的残余见"补完"一节末尾。

#### F5 完成记录

**做了什么**

- `Plugin` 增加 `Protected`（核心插件不能被用户停用/重载/配置禁用，只有可选插件可以）、`Disable`、`Graph()`（节点 + 边：谁需要哪个服务、可选与否、当前是否解析到提供者）。`PluginGraph()` 暴露给前端。
- `NewRuntime()` 拆为 `runtime_boot.go`（建宿主、注册、按配置禁用、启动、核心插件没起来就整体失败并**点名是哪个插件、为什么**）与三组插件定义：`runtime_plugins_data.go`（storage、memory、sessions、checkpoints、approvals、capability、workspace、tools）、`runtime_plugins_agent.go`（agent、workflow、apps、toolset）、`runtime_plugins_features.go`（kanban、taskhub、gateway、scheduler、distill、capability-sync、ide）。共 19 个插件，其中 15 个核心、4 个可选（gateway、distill、capability-sync、ide）。服务以名字发布，消费者用 `Inject/Optional` 声明，不再靠 `NewRuntime` 里的局部变量传递。
- `AppService` 的第一方插件（eino、crush、crushproto、codetools）加入**同一个**宿主，所以 `PluginGraph` 一张图看全；`Close` 关闭一次就按依赖逆序停全部。
- 配置：环境变量 `AGENTGO_PLUGINS_DISABLE=ide,distill` 或 `config.json` 的 `plugins.disabled`。禁用核心插件、未知名字只记日志，不阻止启动。
- 关闭：`Runtime.Close()`（幂等）= `host.Shutdown()`。数据库、网关、调度器、管理任务循环、IDE 监督器/文件监视/终端、同步协程、蒸馏调度都登记为所属插件的 Effect。
- **审计抓到的真问题**：`agent.Runner` 创建了 `ledger.ModelUsageTracer`（每 60 秒落盘的后台循环）却没有任何人关闭它，也没有 `Runner.Close`。已补 `Runner.Close()`（停心跳 + 终止并最后冲刷用量），`ModelUsageTracer.Close` 改为幂等。这个漏洞是泄漏测试第一次运行就发现的。

**验证**

- `runtime_boot_test.go`（9 个测试）：19 个插件全部运行且核心/可选划分正确；所有服务可取；**每条依赖边上依赖者先于提供者停止**（至少核对 20 条边）；`Close` 后数据库已关、**没有任何仍运行 agentgo 代码的新协程**（含网关监听）；同进程内关闭后重建，会话仍在；按配置/环境变量禁用可选插件仍能启动，且图中对应边显示未解析；数据目录不可用时启动失败、错误点名 `"storage"`、且失败的启动不留协程；用户无法停止/重载 `storage/sessions/agent/tools`，能停止并重载 `ide`；**换掉 memory 插件为桩实现，agent 与 toolset 无需改动即可启动，`remember` 工具写入的是桩**。
- `internal/arch/plugin_rules_test.go`（3 个测试）：具体插件（`codetools`、`crushproto`、`engine/crushengine`）只允许 `bridge` 引用（开发探针 `cmd/smoketest`、`cmd/crushproto-probe` 除外）；`plugin`、`engine`、`crushproto` 只依赖标准库；装配文件里不得出现标识符 `AppService`（按 AST 判断，注释不算）。变异验证：往装配文件加 `var _ *AppService`、往 `tools` 加对 `codetools` 的 import，对应测试都失败，还原后通过。
- `go vet ./...`、`go test ./... -count=1` 全绿；`-race`（plugin、ledger、agent、bridge、governance、sessions）无竞争；启动类测试连跑 8 轮无失败。

**第一轮交付时没做的（已在"补完"一节全部处理）**：model 插件化；`eino`/`crush`/`crushproto` 脱离 `AppService`；能力探测；`job_monitor`/终端/Jupyter/sandbox 子进程归属。（上面"19 个插件、15 核心"是第一轮的数字，加入 `model` 后是 20 个、16 核心。）
- 流式回复里的 `usage_observer` 协程随单次流结束，属于单次调用生命周期，不是常驻循环，未处理。

**残余风险**

- 运行时 `Reload` 可选插件（如 `ide`）会改写 `rt` 上的字段，而其他协程无锁读取这些字段。`-race` 通过是因为测试里没有并发读写；若要在界面上放开"随时重载"，需要给这些字段加读锁或改成每次从宿主取服务。
- `capability.NewMatrixCoordinator`、`SubscribeRuntimeHeal` 对事件总线的订阅没有取消；taskhub 没有停止接口；`applog.Init` 会泄漏一个文件句柄（早已存在）。这三项不会在 `Close` 后继续工作（总线随之无人发布），但属于"未显式释放"。
- 同一个 `Runtime` 上创建多个 `AppService` 会因共享宿主而插件重名；生产中一个进程一个，测试用手工构造的运行时或各自独立的运行时，没有出现。
- 以前被忽略的 apps / 动态工具存储初始化错误，现在会让对应（核心）插件失败，从而整个启动失败。这是有意的显式化，但意味着坏数据会从"悄悄少一个功能"变成"启动不了"。

#### 后续：后台命令的退出策略（已决定并完成）

决定：**应用退出时终止后台命令，不保留。** 依据（先核实了事实，纠正我此前的一个错误说法）：`BackgroundTaskCoordinator` 用的是 `InMemoryStore`，**任务状态根本不落盘**（`dataDir` 存了但没被用），所以"保留并在重启后接管"在现有设计下不可能；此前的行为是退出后命令变成无人管理的孤儿进程。因此没有"显式标注常驻任务"这回事，等将来做了持久化再设计。

- `Runner.Close()` → `BackgroundTaskCoordinator.Close()`：先终止所有仍在运行的命令（**整棵进程树**），最多等 3 秒；再带 3 秒期限关闭 eino 的任务 `Manager`。幂等，且不会因为杀不掉的命令卡住退出。关闭后拒绝启动新命令。被终止的命令结果为 `canceled`，错误为 `interrupted: AgentGo exited while the command was running`（eino 没有 `interrupted` 状态，所以用 `canceled` + 原因）。
- 顺带修复两个**早就存在**的缺陷，都是新测试暴露的：
  1. `Stop` 只 `Kill` 了 `cmd.exe`，命令里启动的孙进程继续运行；并且 `Stop` 设的 `canceled` 状态随后被退出码覆盖成 `failed`。现在 Windows 用 `taskkill /T /F`、类 Unix 用独立进程组整组 `SIGKILL`，并且先杀树再取消 context（反过来的话 shell 已死，子进程找不到了）；状态保持 `canceled`。
  2. **Windows 上带双引号的命令被破坏**：Go 把参数里的 `"` 转义成 `\"`，`cmd.exe` 不认，结果 `cmd /C powershell -Command "..."` 只是把命令文本回显出来而不执行（已用最小程序复现，且测试中变异还原后同样失败）。现在 Windows 传原始命令行 `cmd /S /C "…"`。
- 测试（`bgtask_shutdown_test.go` 4 个 + `runtime_boot_test.go` 1 个）：关闭后命令与其孙进程都被终止（孙进程会在 3 秒后写标记文件，写了就说明漏杀）、关闭幂等且拒绝新命令、已结束的命令结果不变、`Stop` 得到 `canceled` 且不留子进程、带引号命令按原样执行、`rt.Close()` 能带走后台命令。变异验证：禁用 `taskkill`（只杀 shell）→ 关闭与 Stop 两个测试失败；还原旧的参数传递方式 → 引号测试及依赖它的测试失败。`go vet`、`go test ./...` 全绿，`-race`（agent、bridge、plugin）无竞争，Linux 目标类型检查通过（未在 Linux 上实际运行进程组逻辑）。

（同一个引号缺陷在另外 3 处的修复，见下一节。）

#### 补完 F5

**1. 引号缺陷在其余 3 处的修复。** 抽出共享包 `internal/shellcmd`（只依赖标准库，登记在 `arch` 的最底层）：`Command(ctx, line)` 在 Windows 传原始命令行 `cmd /S /C "…"`、其它平台 `sh -c`；`Prepare`/`KillTree` 负责整棵进程树。`agent/bgtask.go`（原来自带的一份已删除）、`tools/builtin.go`（`execute_bash`，模型最常用的 shell 工具）、`sandbox/local_seam.go`、`terminal/exec.go` 都改用它。`coderuntime` 传的是脚本路径，Go 对含空格路径的加引号方式本来就对，不受影响，未改。
- 测试：`shellcmd`（带引号命令按原样执行、以引号开头的命令、`KillTree` 终止孙进程且命令退出、对未启动的命令安全）、`terminal`、`sandbox` 各一个带引号命令的回归测试。变异验证：把 `shellcmd` 的 Windows 实现改回旧的参数传递，三个包的测试同时失败。
- **`execute_bash` 没有直接测试**：它必须经过 Eino 的中断/恢复上下文才能调用，单测里构造不出，我没有为它伪造上下文。它现在和另外几处用同一个 `shellcmd.Command`，风险由共享包的测试承担，但这条路径本身仍需要一次端到端手动验证（让智能体跑一条带引号的命令）。

**2. model 插件化。** 新增 `model` 插件（核心，无依赖），提供接口 `ModelSource{ LLM() LLMConfig }`；默认实现 `configModel` 读 config.json 配置、随设置界面即时生效。`memory`、`agent`、`toolset` 三个插件显式 `Inject` 它，依赖图里可见。直接读配置的十几处旧调用（`message.go`、`gateway_backend.go`、`matrix_compose.go`…）不用改：`Runtime.LLMConfig()` 现在先问宿主里的 model 服务，所以换掉插件后它们也跟着变。`Runner.LLMSettings()` 作为只读访问器新增，使测试能观察到 agent 实际使用的模型。
- 测试：换成桩 `ModelSource` 后 `memory/agent/toolset` 照常启动，**agent 与直接读取者都拿到桩的配置**，图里 3 个消费者都有指向 `model` 的已解析边；默认实现下设置界面改模型，agent 立即可见（不需重启）。变异验证：让 agent 绕开宿主直接读 `rt.llm`，替换测试失败。
- 局限：`SetLLMConfig`（设置界面写入）只写默认实现的后备存储。换成别的 `ModelSource` 后，设置界面的修改不会影响它。要让设置界面也走服务，需要给 `ModelSource` 加写接口并定义持久化归属——有意没做，等有第二个真实实现时再定。

**3. `eino`/`crush` 脱离 `AppService`，`crushproto` 显式声明依赖。**
- `eino`：只依赖窄接口 `chatBackend`（一个方法 `sendMessageCore`），通过宿主里的 `brain` 服务取得；不再接收 `*AppService`。
- `crush`：只需要 `*Runtime`（工作区、数据目录、编码节点执行器），构造函数改为 `crushPlugin(rt)`；`AppService.crush` 字段删除（本来只有测试读），改由插件发布 `crush-engine` 服务，`AppService.crushEngine()` 从宿主取。
- `crushproto`：协议适配器要驱动整个应用（会话、运行、审批、LSP 与模型视图），做不到只依赖窄接口，所以改为 `Inject` `brain` 服务，依赖进入依赖图；它是**唯一**仍耦合 `AppService` 的第一方插件，登记在 `arch` 测试的白名单里。
- 架构测试 `TestPluginConstructorsDoNotDependOnAppService`：bridge 里所有返回 `plugin.Plugin` 的函数不得引用 `AppService`；白名单只许缩减——条目一旦不再需要必须删除，条目指向不存在的函数也会失败；并检查 7 个关键构造函数确实被扫描到（防止测试失效）。变异验证：让 `einoPlugin` 重新接收 `*AppService`，测试失败。

**4. 能力探测。** `AppService.Capabilities()` 返回 12 项能力（Eino 聊天、Crush 引擎、Crush 协议服务、代码工具、内置工具、工作流、审批、记忆、记忆蒸馏、IDE、定时任务、网关），每项给出 `available`、依赖的插件和**不可用的原因**：插件未注册时给出启用方法（如"set AGENTGO_GATEWAY_ADDR…"），已注册但没运行时按状态说明（失败 + 错误、被跳过 + 缺什么服务、被停止、被配置禁用、尚未启动）。它从宿主状态现算，不另存一份事实，所以不会和实际运行情况不一致。测试：合成状态逐类验证原因文案；每个能力引用的插件名必须真实存在（防拼写错误）；真实运行时下停止/重载 `ide` 能力随之变化。
- 过程中测试发现一处措辞错误：`Disabled` 在"配置禁用"和"用户 Stop"两种情况下都为真，区别在 `State`（`pending` 与 `stopped`），已按状态判断。
- 前端尚未使用：`Capabilities()` 是新的 Wails 绑定，需要重新生成绑定后界面才能调用；"按能力展示"属于 F6。

**5. 其余子进程归属。** 全仓异步启动子进程的位置共 6 处，逐一核对：`agent/bgtask.go`（已处理）；`ideruntime/process.go` ×2（归 `ide` 插件的 supervisor，`Close` 逐个终止，Windows 上另有 Job Object 兜底）；`engine/crushengine/supervisor.go`（归 `crush` 插件的 Effect）；`sandbox/local_seam.go`（同一函数内 `Wait`，受超时约束，同步）；`sandbox/jupyter`（无人引用）。另有 `tools/job_monitor.go` 用 `CombinedOutput`，不在上面的 `Start` 搜索里，但它是后台常驻的，**这是遗漏最多的一处**，已处理：
- 任务状态原来是包级全局变量，任何 Registry 都能看到、停止别人的任务，也无处统一结束。现在由 `Registry` 持有 `jobMonitor`；`Registry.StopJobs` 终止本 Registry 启动的全部任务（整棵进程树，用 `cmd.Cancel` 钩子 + `WaitDelay`），拒绝新任务，不再触发唤醒回调（没人可唤醒），等待最多给定时间。`Runner.Close` 调用它。
- 顺带修复一个**早就存在的数据竞争**（`-race` 首次跑 `tools` 包时暴露）：`start` 把后台协程正在修改的 `*MonitoredJob` 直接放进返回值，框架在工具函数返回后才序列化，与 `run` 写 `FinishedAt` 并发。现在所有对外输出和唤醒回调都使用锁内取得的快照。
- 测试：关闭后任务与其子进程都被终止、不唤醒、拒绝新任务；两个 Registry 互不影响；nil/空闲 Registry 安全；`rt.Close()` 能带走任务。变异验证：取消钩子退回只杀 shell → 子进程存活测试失败；关闭期间仍唤醒 → 不唤醒测试失败。

**验证。** `go vet ./...`、`go test ./... -count=1`（36 个包全部通过）；`-race`（agent、bridge、tools、shellcmd、plugin、sandbox、terminal、governance、sessions）无竞争；运行时装配类测试连跑 6 轮无失败；Linux 目标类型检查通过（进程组代码路径只检查了类型，没有在 Linux 上运行）。

**登记在案的残余（F5 之后仍然成立）**
- `crushproto` 仍耦合 `AppService`（白名单，只许缩减）。要摘掉，需要把会话/运行/审批/LSP/模型视图从 `AppService` 抽成独立的应用服务——这是个比 F5 更大的重构，建议在 F6 之后有真实需求时再做。
- 设置界面写模型配置只作用于默认 `ModelSource`（见上）。
- `Reload` 可选插件会改写 `Runtime` 字段而别处无锁读取（第一轮已记录）；目前界面只对 `ide` 等可选插件开放重载，未放开前不会触发。
- `execute_bash` 的带引号命令没有单元测试（见上）。
- 总线订阅未取消、`taskhub` 无停止接口、`applog.Init` 泄漏文件句柄（第一轮已记录）。
- `usage_observer` 的单次流协程不是常驻循环，未处理。

**回滚**：改动集中在 `runtime_boot.go`、`runtime_plugins_*.go`、`plugin/host.go`（新增导出，不改旧行为）、`plugins.go`（`initPlugins` 在 `rt.host` 非空时共享宿主，否则退回旧行为）。旧 `NewRuntime` 的原文在 git 的 `init` 提交里。

### F6：跨界面验证，再扩展插件生态

- [x] 同一宿主进程提供 GUI 与 TUI 连接；独立 CLI 作为客户端接入该宿主。（桌面端/headless 是宿主，`agentgo-tui` 默认附着；见 F6 完成记录）
- [x] 本地连接明确发现、鉴权、连接断开与订阅恢复机制。共享数据库目录不等于共享活跃运行。（发现/鉴权/断线检测完整；"订阅恢复"仅限未决权限请求重放 + 快照重读，**不是**透明重连，见残余）
- [x] TUI 发起 → GUI 批准 → TUI 续跑 → 重连恢复 → 查看修改及验证结果。（协议层 + 真实模型端到端均已验证，见 F6 完成记录"第二轮"；续跑输出在续跑结束后一次性出现，非逐字流式；终端内人工操作未做）
- [~] 模型配置同源；界面按能力探测展示支持项，不伪装未实现功能。（附着的 TUI 用宿主的模型；`/host/info` 与 `Capabilities()` 可读；前端未接、Wails 绑定未重新生成）
- [ ] 上述路径稳定后，再做前端槽位、manifest、安装升级与第三方 sidecar。（有意未开工：路径尚未经真实模型端到端验证）

验收：一个真实小项目完成跨界面修复和测试，保留运行事件、审批记录、文件差异与命令结果。真实模型测试需复用现有配置，不在文档和日志暴露密钥。**用真实模型在小项目上已通过一次**（第二轮记录）；仍缺：终端里的人工附着操作、`go test` 级别的测试用例修复场景。

#### F6 完成记录（第一轮：单宿主 + 跨界面审批）

**要解决的问题**：TUI 发起的运行卡在 TUI 的权限弹窗里（goroutine 阻塞在 `Emitter.RequestPermission`）。桌面端批准同一个审批时，队列只记一次决定，续跑在桌面端的调用里完成——TUI 弹窗仍挂着，看不到结果，之后在 TUI 里点批准只会得到"Approval could not be recorded"。而这三个入口各自 `NewRuntime()`，待决运行只在发起它的进程内存里，所以另一个进程连"续跑"都做不了。

**设计（两件事）**：
1. **单宿主**（`internal/hostlink`，仅标准库）：桌面端/headless 里的 `host` 插件监听 `127.0.0.1:0`，把 Crush 协议和 `/host/info` 放在令牌鉴权后面，并把地址+令牌写入数据目录的 `host.json`。客户端 `Discover`＝读文件并用令牌 `ping`；**不信任 pid**（会复用），能 ping 通才算活。每个数据目录只允许一个活宿主（`O_EXCL` 创建 + 陈旧文件顶替；第二个进程插件失败并说明原因，但仍作为普通进程运行）。`agentgo-tui` 默认附着：把 `hostlink.Proxy(info)`（自动加令牌、不缓冲 SSE）当作 `http.Handler` 交给 `embedtui.Run`——**Crush 一行没改**。宿主消失 → `Monitor` 3 次 ping 失败 → TUI 明确退出并提示用 `-continue` 恢复。
2. **审批中继**（`approvalRelay`，进程内）：`resolveApproval` 是唯一落账入口；决定写入队列后立刻 `markDecided`（TUI 弹窗据此关闭），续跑完成后 `markCompleted`（TUI 渲染结果）。`crushRunner.approve` 改为 `AwaitPermission(req, 外部决定)`：TUI 先答就自己落账，别的界面先答就等中继并展示"该审批已由 X 处理"；同时答则队列保证只有一个赢家，输家转为展示赢家的结果。

**改动**：`crushproto`（`AwaitPermission`、未决权限重放：订阅者在加锁下拿到未决请求快照）、`bridge/approval_relay.go`、`approval_resume.go`、`crushproto_runner.go`、`hostlink_plugin.go`、`runtime_boot.go`（`DefaultDataDir()`）、`capabilities.go`（`host.attach`）、`cmd/agentgo-tui/main.go`（附着/独立两条路径，`-attach`/`-standalone`）、`arch`（`hostlink` 入基础层且仅标准库；`hostlinkPlugin` 登记进 `appServiceCoupled` 白名单，写明原因）。

**验证**：
- `crushproto`：外部决定唤醒等待并关闭弹窗；已决定的请求不会被重放；重连客户端第一帧是未决权限请求。
- `bridge`：`TestDesktopApprovalContinuesATUIRunAcrossAReconnect`（TUI 经真实 HTTP/SSE 发起 → 断线重连、弹窗恢复 → 桌面批准 → TUI 弹窗关闭、转录里有续跑文本和决定者、工具恰好续跑一次）；拒绝路径（不执行）；`TestSimultaneousDecisionsResumeOnce`（TUI 与桌面同时答 ×6，只续跑一次）；中继的"先决定后观察/取消/超时"。
- `hostlink`：无令牌/错令牌/无 scheme/Basic 均 401，非回环 `Host` 头 403（防 DNS 重绑定），代理加令牌且不缓冲流，陈旧文件=无宿主，端口被别的监听者复用（令牌不符）=无宿主，第二宿主被拒、陈旧可顶替、旧宿主迟到的清理不会删新宿主文件，Monitor 能发现宿主消失且取消时不误报。
- **变异验证**：去掉 `resolveApproval` 对中继的通知 → 桌面批准与拒绝两个测试失败；令牌校验被旁路 → 鉴权测试失败；`release` 不核对令牌 → 迟到清理测试失败。
- **真实跨进程实测**：启动真实 `agentgo-headless`（临时数据目录）→ `host.json` 出现 → 无令牌 401、带令牌 200、`/host/info` 返回能力（`host.attach`、`approvals`、`chat.eino` 可用）→ 强杀后文件残留，`agentgo-tui -attach` 判定为"无宿主"→ 新宿主顶替旧文件 → 第二个宿主启动**不会**改动文件。`-attach` 无宿主时明确报错退出。
- 全量 `go vet ./...`、`go test ./...` 通过（修复了一个由新插件引起的 `TestCapabilitiesNameRealPlugins` 已知名单）。

**未验证 / 残余（如实登记）**：
- **没有在真实终端里手动跑过附着后的 TUI**（需要人坐在终端前）；以上是协议层 + 进程级验证。Crush 客户端自身在 SSE 断开后是否自动重连、是否重新拉取消息，**没有核实**——所以"重连恢复"目前＝重启 TUI 并 `-continue`，宿主按快照 + 未决权限请求重放让其还原，而**不是**透明续接。SSE 没有 Last-Event-ID 式的逐事件回放。
- **没有真实模型/真实小项目的端到端**（验收条款未满足）。续跑是一次性返回，不是逐字流式；"文件差异、验证结果"没有在跨界面路径上核对。
- 中继是进程内的：**宿主之外的进程**（独立模式的 TUI、第二个桌面实例）与宿主之间仍只共享数据库，对方批准后这边的弹窗不会关。附着模式正是为避免这种情况。
- 桌面端现在**默认**开一个回环监听（令牌鉴权，`AGENTGO_HOSTLINK=0` 关闭）。令牌在 `host.json` 里，Windows 下 `0600` 不生效，靠用户目录 ACL，与 `config.json` 里存 API Key 是同一信任边界。`AGENTGO_CRUSHPROTO_ADDR` 的无鉴权口（给原版 `crush -H` 用）保持原样、仍是显式开启。
- 独立模式下的 Azure 环境变量覆盖仍会通过 `SetLLMConfig` **写入共享的 `config.json`（含密钥）**——这是原有行为，附着模式不触发；是否改为"仅本进程生效"需要你决定。
- 其他入口（`mcphost`、`llmtest`、ACP）没有接入附着。前端没有展示 `Capabilities()`（需重新生成 Wails 绑定）。
- 尚未回答的产品问题仍在：拒绝审批后是否告诉模型并改道；降级兜底回复是否排除出记忆。

#### F6 完成记录（第二轮：真实模型跨界面端到端）

`TestE2ECrossSurfaceFixWithDesktopApproval`（`crosssurface_e2e_test.go`，门控：`AGENTGO_CROSSSURFACE_E2E=1` + 空的 `XDG_DATA_HOME` + 现有 Azure 环境变量 + gopls；数据全部落在临时目录，日志不含密钥，跑完删除目录）。流程：宿主=真实 `NewRuntime`+`NewAppService`；"TUI"=用 `hostlink.Discover` + `Proxy` 经令牌鉴权端点发起运行；"桌面"=进程内轮询审批队列并调用 `ResolveApproval`；**未开 YOLO**，审批是真问的。

结果（一次真实运行，约 28 秒，PASS）：
- 小项目 `main.go` 的编译错误被真实修复，`go build ./...` 通过。
- 桌面端批准了 2 个审批（`code_edit`、`run_batch_script`）；TUI 收到 2 次权限弹窗、2 次"已批准"关闭通知——**弹窗数与桌面批准数一致**。
- 审批记录：两条均为 `approved`、`resolved_by=desktop_user`。
- TUI 视角：所有工具调用都有配对结果；文件历史含 `main.go` 修复前后版本（"Modified Files" 数据）。
- 运行事件日志：事件序列含 `approval_request` 与多段 `done`（每个续跑段各一个提交点），最终状态 `completed`，没有遗留"待审批"。
- 观察（非缺陷但记录）：链式审批时日志里是 `done,status,done,status,done`，续跑段内部没有逐步事件——与 F4 已登记的"续跑段逐步事件未做"一致。

仍然没有的：真实终端里人工操作附着后的 TUI（Crush 客户端断线后的自动重连行为仍未核实）；以"让失败的测试通过"为目标的修复场景；变异验证只在单元层做了（真实模型那次没有再做一遍去掉中继的对照）。

**回滚**：`AGENTGO_HOSTLINK=0` 即关闭宿主端点；`agentgo-tui -standalone` 即旧行为；中继改动集中在 `approval_resume.go` 两行 + `crushproto_runner.go` 的 `approve`。

## 5. 首个开工任务

**先完成 F0，再只实现 F1。** 优先检查：

- `internal/bridge/plugins.go`
- `internal/bridge/codetools_plugin.go`
- `internal/bridge/app_service.go`
- `internal/bridge/workspace_runtime.go`
- `internal/bridge/crushproto_lsp.go`
- `internal/plugin` 与 `internal/compose/service`
- 现有 `codetools_plugin_test.go`、`plugins_test.go` 与 workspace 测试

F1 的交付是显式服务依赖、受控工作区重绑定、资源清理及针对性回归。此阶段保持现有会话格式、审批协议和前端结构，避免把所有迁移塞进一次改动。

验证顺序：先相关包测试，再适用的架构检查和全量 Go 回归；只在修改对应入口时构建 TUI/无头或前端。竞态检测在现有工具链支持时执行，不能执行要记录原因。基线已有失败单独报告。

## 6. 完成记录

每阶段填写：实际改动、验证命令与结果、跳过项、遗留风险、回退方式。未完成的验收不勾选。

| 阶段 | 状态 | 证据 |
|---|---|---|
| F0 | ✅ 完成 | 基线全绿；唯一装配入口=`plugin.Host`；`compose/*` 死代码；治理续跑回归测试已加并经变异验证；备份 `D:\dev\backup\agentgo-f0-baseline` |
| F1 | ✅ 完成 | 切换/回滚/占用/去重/Close 测试 + 变异验证；全量 build/vet/test 与 race 通过 |
| F2 | 🟡 主体完成 | 审批漏洞修复、特性登记、pre/post hook、降级显式化已交付并有测试；风险等级迁移、execute 环绕点未做（见 F2 完成记录） |
| F3 | 🟡 F3a 完成 | 运行事实日志、提交点、重连续读、`GetMessages` 取最老 n 条的缺陷修复；F3b（投影/迁移）有意推迟，F3c（旧路径接入）未做 |
| F4 | 🟡 主体完成 | 统一入口 `resolveApproval`（连续审批、原子认领、取消贯通、显式失败、续跑进日志）+ 待决运行持久化、重启采纳/对账、到期释放；未做：`cancelled` 状态、续跑段逐步事件、跨进程批准（F6）|
| F5 | ✅ 完成（有登记的残余）| `NewRuntime` 拆为宿主 + 20 个插件（16 核心/4 可选）；model 与 memory 均可替换且有测试；依赖图 + 状态 + 失败原因 + 能力探测（`Capabilities()`）；关闭按依赖逆序，无遗留协程，后台命令与 `job_monitor` 任务退出即终止（整棵进程树）；架构约束（`crushproto` 一项白名单）；顺带修复：`ModelUsageTracer` 无人关闭、Windows 带引号命令被破坏（共享包 `shellcmd`，4 处）、`job_monitor` 的数据竞争与全局状态。残余：`crushproto` 仍耦合 `AppService`、设置界面只写默认模型源、`execute_bash` 无单元测试 |
| F6 | 🟡 第一轮完成 | 单宿主（`hostlink`：发现/令牌鉴权/回环守卫/断线检测）+ `agentgo-tui` 附着 + 审批中继（桌面批准 → TUI 关弹窗并显示续跑结果；同时决定只续跑一次）+ 未决权限重放；协议层与跨进程实测通过，含变异验证。真实模型跨界面端到端已通过（修复编译错误，2 次桌面审批，TUI 弹窗同步关闭，审批记录与运行日志完整）。**未做**：终端内手动验证、前端能力展示、插件生态扩展 |
