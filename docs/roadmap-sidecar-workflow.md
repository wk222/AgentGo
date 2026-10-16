# 路线：常驻 Sidecar + VS Code 扩展 + 可视化多智能体工作流

> 状态：设计阶段（未开工）。结论来自 2026-10 的方案讨论，未做实测。

## 1. 总体决策

- 不新开项目，在 `agentgo` 仓库内扩展；`agentgo.exe --serve` 作为常驻 Sidecar（不另做 daemon 可执行文件）。
- IDE：先做 **VS Code 扩展**（Webview 复用现有 Vue 前端），不 fork Code-OSS；确需独立 IDE 时再考虑 Theia。
- Zed 仅作为 ACP 次要入口（见 `docs/zed-acp.md`），Zed 无法承载自定义面板。
- 事件驱动唤醒，**不做默认定时轮询**（避免浪费 token）。
- 同一个 Sidecar 同时服务：VS Code 扩展、Wails 托盘、聊天渠道（"龙虾"形态）。

## 2. 目录规划

```
cmd/agentgo/            新增 --serve 模式
internal/gateway/       本地 HTTP/WebSocket、随机令牌、单实例锁(仅 127.0.0.1)
internal/trigger/       触发器：进程退出 / 标记文件 / 日志匹配 / webhook
internal/channels/      聊天渠道适配器（先一个：飞书或 Telegram）
internal/memory/        持久记忆
internal/workflow/      工作流规格、校验器、执行器、版本
extensions/vscode/      VS Code 扩展
```

单文件超过 900 行就按功能拆分。

## 3. 工作流规则（用户已确认）

1. **运行期间不可改**：每次运行绑定一个**不可变的版本快照**（`workflow_id + version`）。运行中的任何编辑不影响当前运行。
2. **可在主对话里修改**：用户在主对话里说"把第三步换成……"，智能体产出**新版本**（version+1），状态为待批准。
3. 新版本的生效方式（运行中收到修改时，默认让用户选）：
   - 当前运行继续跑完旧版本，新版本用于下一次运行（默认）；
   - 取消当前运行，从最近检查点按新版本重跑（仅当检查点前的节点未被改动时允许）。
4. 新生成/新修改的版本必须经用户批准才能运行。
5. **每个节点可指定不同模型**。

## 4. 节点与模型

节点类型：`agent`、`tool`、`parallel`、`condition`、`human_approval`、`wait_event`、`subworkflow`。

模型配置：
- 节点里写**模型别名**（如 `planner`、`worker`、`cheap`），不写具体密钥/端点。
- 别名到真实模型的映射放在本机配置（密钥只走环境变量或本机配置，不入库不提交）。
- 校验器检查：该别名是否存在、是否支持工具调用；不满足则报错让智能体修复。
- 已知约束：Azure `gpt-6-luna` 需 `AGENTGO_REASONING_EFFORT=none` 才能稳定调用工具。
- 可配置失败回退模型与单节点超时、token 上限。

## 5. 治理

- 总节点数、总调用数、总 token、总耗时设上限，超限暂停并通知。
- 限制子工作流深度，禁止无限自生成。
- 节点声明可用工具；写文件/执行命令仍走现有审批。
- 网页、邮件等外部内容视为不可信，不得直接改写工作流或触发工具。
- 聊天渠道只认白名单账号；接口只绑 127.0.0.1 并带令牌。

## 6. 可视化

- Vue Flow（保留 Vue 前端，Wails 与 VS Code Webview 共用）。
- 设计态：展示、拖拽、改节点提示词，保存即产生新版本。
- 运行态：订阅运行事件，节点显示 等待/运行/完成/失败/待审批；点击看输入输出。

## 7. 分阶段

1. Sidecar 单实例 + 本地接口（HTTP/WebSocket、令牌、锁文件）。
2. 触发器引擎（进程退出、标记文件优先），唤醒"恰好一次"。
3. 工作流规格 + 校验器 + 版本表 + 执行器（agent/tool/parallel/condition），事件写入运行日志。
4. `create_workflow` / `revise_workflow` 工具 + 校验失败修复循环。
5. 前端 Vue Flow（设计态、运行态）。
6. VS Code 扩展最小版（连接、Webview、通知）。
7. `wait_event`、`human_approval`、子工作流；聊天渠道；记忆与技能。

## 7.1 实际进展与重要发现（2026-10-10）

**仓库里已有大量基础设施，不要重复造：**
- `internal/gateway`：REST + SSE，含 `/v1/chat/completions`、`/api/v1/chat/stream`、`/api/v1/tasks/{id}/events`（可回放）、`/api/v1/workflows`（flowgram 读写与运行）；`authMiddleware` 支持 Bearer/X-AgentGo-Token。
- `internal/taskhub`、`internal/scheduler`、`internal/workflow`（含 parallel/merge executors、flowgram store）、`internal/channels`、`internal/memory`、`internal/skills`、`internal/checkpoint`、`internal/kanban`。
- 网关原先只在设置了 `AGENTGO_GATEWAY_ADDR/PORT` 时启动，且主程序总会开 Wails 窗口，没有无窗口模式。

**已完成（阶段 1 的最小版）：**
- `agentgo.exe --serve`：无窗口常驻；默认 `127.0.0.1:8787`，自动生成随机令牌；写 `serve.json`（addr/token/pid）供客户端发现；已有实例存活（`/health` 可达）时第二个实例直接退出；SIGINT/SIGTERM 清理。
- 已实测：无令牌访问受保护接口返回 401，带令牌 200，第二实例被拦。
- 发布版不再默认打开 DevTools Inspector（仅 `--dev`/`AGENTGO_DEV=1`）。

**已知限制：**
- Windows 下 `0600` 不会真正收紧 ACL，`serve.json` 继承数据目录权限；令牌保护依赖本机用户隔离。数据目录目前在 C 盘 `AppData\Local`。
- 触发器引擎（进程退出/标记文件/日志匹配/webhook）尚未做；`scheduler` 是否可复用待核对。
- 现有 workflow 是 flowgram 格式，需核对与"版本快照 / 节点级模型别名 / 智能体生成"方案的差距，再决定扩展还是新建规格。

## 7.2 现有 scheduler / workflow 核对结果

- `scheduler`：只有**固定间隔**任务（Create/List/Due/BumpNext），不是事件驱动，不能当触发器引擎。
- `workflow` 引擎基于 Eino compose，**已有**：agent / adk_agent / llm / tool / bash / http / condition / loop / parallel / merge / subworkflow / wait_signal / emit_signal / ask_user(hitl) / debate(multi_agent) / notify / monitor / lease / cron 等 85+ 节点注册。
- 检查点存 SQLite（`checkpoint/sqlite_store.go`），`wait_signal` 与 `ask_user` 通过 compose interrupt 持久化，`bridge.ResumeWorkflow` 可续跑。→ 长时间等待在引擎层面具备基础。
- `SignalBus` 是进程内存实现；`monitor` 节点只比较输出指标，不监听进程/文件。
- **确认缺失**：
  1. 外部事件入口（HTTP 触发 signal / resume）与 进程退出 / 标记文件 / 日志匹配 监听；
  2. 工作流版本快照（store 无 version）；
  3. 智能体侧的 create_workflow / revise_workflow 工具（现有 graph_tool 只是把已保存工作流暴露为工具）；
  4. 节点级模型别名（尚未确认 llm/agent 节点现在如何选模型，需单独核对）；
  5. Vue Flow/可视化运行态（现有为 flowgram 格式，前端是否已有编辑器待核对）。

**修订后的阶段 2**：不新建引擎，在 `internal/trigger` 里做事件监听，命中后调用已有的 signal emit / `ResumeWorkflow`；`POST /api/v1/triggers`、`POST /api/v1/signals` 挂到现有 gateway。

## 7.3 阶段 2 已完成：触发器引擎（2026-10-10）

- `internal/trigger`：process_exit（Windows 阻塞等待进程句柄并取得退出码，非 Windows 每 2s 探活）、file（每 1s stat 标记文件，本地调用不耗 token）、log_match（只看登记之后新增的行，处理截断/轮转）、webhook。
- 持久化到 SQLite `triggers` 表；`MarkFired` 用 `UPDATE … WHERE status='armed'` 做原子认领 → 同一触发器只唤醒一次（跨 goroutine/跨进程）；`delivered` 标记使"已触发未投递"在重启后重投（至少一次投递，认领保证不重复触发）。
- 动作：`wake_session`（`taskHub.Start("trigger", session, prompt+事件)`）、`emit_signal`（`SignalBus.Emit(run_id, name)`）。
- 网关：`POST/GET /api/v1/triggers`、`GET/DELETE /api/v1/triggers/{id}`、`POST /api/v1/triggers/{id}/fire`、`POST /api/v1/signals`（经可选接口 `TriggerBackend` 注册，不破坏现有 Backend）。
- 已实测（`--serve` 真实进程）：创建→armed→写标记文件→fired+delivered；二次 fire 返回 409；无令牌 401；杀进程重启后仍能触发。
- 8 个单元测试覆盖：校验、标记文件、日志只看新行、webhook 并发恰好一次、取消、过期、重启重挂/重投、进程退出码。
- `AGENTGO_DISABLE_TRIGGERS=1` 可在共享数据目录的第二个进程里关闭监听，避免重复。

**未完成/未验证**
- 未验证 `wake_session` 之后智能体是否真的跑完一轮（冒烟测试用的是不存在的 session，只确认 `taskHub.Start` 接受）。
- 智能体侧的 `watch` 工具还没做（目前只能通过 HTTP 登记触发器）。
- `emit_signal` 只对内存 `SignalBus` 生效；对已中断持久化的 `wait_signal` 需走 `ResumeWorkflow`，尚未接。
- 远端（MUS）实验需通过 webhook 或 SSH 阻塞监控回传。

## 7.4 `watch_event` 工具与真实端到端验证（2026-10-10）

- 智能体工具 `watch_event`（create/list/cancel）：触发器绑定到**调用者自己的会话**（取自 `governance.SessionIDFromContext`），超时默认 24h、上限 168h，单会话最多 20 个 armed，跨会话不能取消。
- 触发后在原会话落库两条消息：`system`（事件说明）+ `assistant`（唤醒后的回复）。注意：`session` 必须真实存在于 `chat_sessions`（`chat_messages` 有外键）。
- **真实 e2e 通过**（Azure gpt-6-luna，`--serve`）：智能体在对话里调用 `watch_event` 监视标记文件 → 本轮结束 → 写入标记文件 → 触发器 fired/delivered → 后台任务唤醒智能体 → 智能体用 `read_workspace_file` 读取文件并回复"实验完成: acc=0.93 loss=0.21"。
- 治理生效：`path` 超出工作区根目录会被 `PathPolicyStage` 拦截（工作区根取 `AGENTGO_WORKSPACE_ROOT`，否则 workspaceHint，否则 cwd）。所以只能监视工作区内的文件；**监视工作区外的实验输出目录需要先配置/扩大工作区根**。
- 已知问题（既有行为，未改）：`GenerateStream` 的内容里混有工具返回的原始 JSON，导致落库的 assistant 文本带前缀噪音；`cron` 类后台任务同样不写入会话历史。
- 已知问题：被强杀的进程会留下状态为 `running` 的旧任务行（未做任务恢复）。

## 8. 待确认

- 现有前端对 Wails 绑定的依赖深度（决定传输层改造量）。
- 现有 `crushproto` HTTP 路由能否复用为本地接口。
- 长会话续跑时的上下文截断/摘要策略。
- 无人在线时待审批的超时策略。
- 远端实验（MUS）回传：webhook 回调或本机 SSH 阻塞监控。
