# AgentGo 施工计划（2026-06）

## 北极星（一条故事，三个月不换）

**桌面里用自然语言做出可打开的 Inner App，并能稳定调用工作流 / 对话 API。**

验收：用户说需求 → `build_inner_app_iteratively` 或侧栏新建 → 应用面板可打开 → `ping` 或 `chat` 或 `workflow_run` 至少一种可用。

---

## 架构原则（不对齐 PyBot 全文，只守三条）

| # | 规则 |
|---|------|
| 1 | 除 `bridge` 外，任何 `internal/*` **不得 import `bridge`** |
| 2 | **资产**（`apps` / `workflow` / `tools`）**不得 import `agent`** |
| 3 | **`agent` 不得 import `bridge`** |

**资产 / 编排 / 消费** 三分法（天天用）：

- **资产**：磁盘 + DB 定义（`apps/`、`workflow` 表、工具定义）
- **编排**：`internal/agent`（Runner、模式、Matrix、子 Agent）
- **消费**：`bridge` + `frontend/dist`（IPC 薄层）

完整 L0–L4 登记表见 `ARCHITECTURE.md`；CI 以 `go test ./internal/arch/...` 为准。

---

## 阶段 A — Inner App 真闭环（当前施工）

| 任务 | 状态 | 说明 |
|------|------|------|
| A1 脚手架 + 侧栏新建 | ✅ | `scaffold_inner_app`、`DesktopService` |
| A2 确定性 verify + auto-fix helpers | ✅ | `verify_inner_app`、`AutoFixBundle` |
| A3 **app_builder 子 Agent**（限定 4 工具） | ✅ | `agent/app_builder.go` |
| A4 `build_inner_app_iteratively` 接 LLM 环 | ✅ | `agent/app_builder_tools.go` → `BuildInnerAppFull` |
| A5 种子 `app_builder` 进 SubagentRegistry | ✅ | `subagent_registry.go` SeedDefaults |
| A6 集成测 scaffold + verify | ✅ | `apps/scaffold_test.go` |

**不做（阶段 A）**：`api.py` 沙箱、K8s、完整 PyBot verify_app LLM 评分。

---

## 阶段 B — 工作流一条 happy path

| 任务 | 状态 |
|------|------|
| B1 模板 DAG：LLM → Tool → Notify 可保存可跑 | ✅ `wf_happy_path` + 侧栏「加载演示模板」 |
| B2 运行历史 + interrupt 恢复 UI 走通 | ✅ 历史列表 + `workflow:notify` 事件；HITL 见 `workflow_hitl_test` |
| B3 workflow 工具节点走统一 `GovernedInvoke` | ✅ `ToolsBridge` + `ComposeToolMiddleware`；fallback `GovernedInvokeJSON` |

---

## 阶段 C — 治理与心智负担

| 任务 | 状态 |
|------|------|
| C1 dynamic Python / workflow 工具 / Matrix 子 Agent 同一审批语义 | 待办 |
| C2 Matrix 只维护 `matrix-orchestration.md` 一条官方路径 | ✅ 文档 + `matrix_followup` |
| C3 收窄 arch 测试为「三条硬规则」可选 | 待办 |

---

## 明确后置

- IM 渠道、Browser 工具、33 PyFlow 节点、2071 式测试规模  
- `matrix_compose` 迁出 `bridge`（等 Matrix 成日常入口）  
- 论文级 Admin 状态机、Capability 路由树  

---

## 本周提交检查清单

```powershell
cd agentgo
go test ./internal/arch/... ./internal/apps/... ./internal/agent/... -count=1
go build -o bin\agentgo.exe .\cmd\agentgo
```

手动：设置 API Key → 对话「用 build_inner_app_iteratively 创建 demo_chat」→ 应用面板打开。
