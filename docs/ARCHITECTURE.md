# AgentGo 架构分层

与 PyBot `core/modes/system_model.py` 对齐的 **机读 + 测试守卫** 模型。

## 依赖方向

```
L5 cmd/agentgo
  → L4 internal/bridge   (Wails IPC、Runtime 装配)
    → L3 internal/agent  (模式、Matrix、ADK Runner)
      → L2 internal/*    (tools, skills, workflow, apps, admin, …)
        → L1 internal/*  (governance, memory, capability, …)
          → L0 internal/* (db, sessions, workspace, applog)
```

**规则**：高层只能 import 同层或更低层；`cmd` 只能 import `bridge`。

**三条硬规则**（优先守，比登记表更重要）：

1. 除 `bridge` 外，任何包不得 `import bridge`
2. `apps` / `workflow` / `tools` 不得 `import agent`
3. `agent` 不得 `import bridge`

施工计划见 [`plan.md`](./plan.md)。

## 包注册表

见 `internal/arch/layers.go` 中的 `PackageLayer`。新增 `internal/<pkg>` 时必须登记，否则 `go test ./internal/arch/...` 失败。

## bridge 职责（L4 消费层）

- 只做：**IPC 适配**、**Runtime 组装**、**把接口注入 agent pipeline**
- 领域逻辑应在 L2/L3：
  - Inner App 桌面操作 → `internal/apps/desktop.go`
  - 页面 HTML → `internal/apps/page_html.go`
  - Matrix 开关与管线顺序 → `internal/agent/matrix_config.go`、`matrix_followup.go`

## 五大产品概念（对外）

| 概念 | 包 |
|------|-----|
| Tools | `internal/tools` |
| Skills | `internal/skills` |
| Agents | `internal/agent` + `subagent_registry` |
| Workflows | `internal/workflow` |
| Apps | `internal/apps` |

## CI 建议

```powershell
go test ./internal/arch/... ./internal/apps/... -count=1
```
