# App Matrix 编排（官方路径）

能力总线事件（`workflow.done` / `tool.compiled` / `app.registered`）在 **`AGENTGO_MATRIX_AUTO_FOLLOWUP=1`** 时触发编排。

## 唯一推荐的三级回退

| 顺序 | 名称 | 实现 | 何时跳过 |
|------|------|------|----------|
| 1 | **Supervisor** | `agent.RunMatrixSupervisor` + `adk.NewAgentTool` | `AGENTGO_MATRIX_SUPERVISOR=0` 或无 API Key |
| 2 | **Compose** | `bridge.RunMatrixCompose`（plan→execute→summarize） | `AGENTGO_MATRIX_COMPOSE=0` |
| 3 | **Legacy** | 单次 `Runner.Generate`（coordinator 提示） | 前两级已有 summary |

管线入口：`agent.RunMatrixFollowup`（`internal/agent/matrix_followup.go`）。  
开关集中：`internal/agent/matrix_config.go`。

## 环境变量（一张表）

| 变量 | 默认（未设置时） | 含义 |
|------|------------------|------|
| `AGENTGO_MATRIX_AUTO_FOLLOWUP` | off | **总开关** |
| `AGENTGO_MATRIX_SUPERVISOR` | 随 AUTO_FOLLOWUP on | `1` 强制开，`0` 强制关 |
| `AGENTGO_MATRIX_COMPOSE` | 随 AUTO_FOLLOWUP on | `1` 强制开，`0` 强制关 |
| `AGENTGO_MATRIX_EMIT_EVENTS` | 随 AUTO_FOLLOWUP on | 子 Agent trace / HITL |

## 不推荐

- 在业务代码里直接读多个 env 自行拼顺序——应调用 `MatrixSupervisorEnabled()` / `MatrixComposeEnabled()` / `RunMatrixFollowup`。
- 同时开自定义编排与 Matrix 管线而不走 capability 事件。

## HITL

Supervisor 返回 `PendingApproval` 时管线 **停止**，由 `bridge/matrix_hitl.go` 注册暂停，桌面审批后 `ResumeMatrixSupervisor` 续跑。
