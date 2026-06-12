# PyBot 四层树 vs AgentGo 对照（2026-06）

PyBot 的「几层树」有两套说法，不要混用：

1. **架构依赖树 L0→L3**（`core/modes/system_model.py`，机读 + 架构守卫测试）  
2. **五大产品概念**（tools / skills / agents / workflows / apps）——挂在 L2/L3，不是第四层架构  
3. **论文/差距文档里的 L5** 常指 **「应用域」能力带**（Inner App + Matrix 九工具），不是 L0–L3 之外的第五架构层  

## 架构四层（PyBot）

| 层 | 名称 | 主要职责 | 典型包 |
|----|------|----------|--------|
| **L0** | Runtime Foundation | 启动、会话脊柱、上下文引擎、LLM 路由、插件 SDK | `systems/runtime`, `session`, `context`, `llm` |
| **L1** | Core Systems | 治理、记忆蒸馏、CapabilityBus、中间件、执行沙箱、观测、渠道/MCP | `governance`, `memory`, `capability`, `middleware`, `execution` |
| **L2** | Asset Domains | 可序列化领域对象：工具/技能/智能体/工作流/应用模板 | `assets/tools`, `skills`, `agents`, `workflows`, `apps` |
| **L3** | Product Modes | 根身份（assistant / app_matrix / admin）+ ExecutionCanvas + 编排运行时 | `modes/`, `systems/agents`, `systems/apps` |
| **L4*** | Consumer | Web/API/CLI 消费面（不算 core 层内） | `web/`, `api_server.py` |

依赖规则：**只能向下 import**，禁止反向。

## AgentGo 是否有对应层？

AgentGo 是 **Go 单体 + Wails 桌面**，没有 Python 式 `system_model` 守卫，但模块职责可一一对照：

| PyBot 层 | AgentGo 大致对应 | 覆盖度 |
|----------|------------------|--------|
| **L0** | `internal/sessions`, `internal/workspace`, `internal/bridge/runtime.go`, `internal/agent/runner.go`, `internal/db` | ✅ 会话/工作区/启动；🔶 无完整 Context Engine / 事件账本压缩 |
| **L1** | `internal/governance`, `internal/memory`, `internal/capability`, `internal/applog`, `internal/gateway` | 🔶 治理管道已加强；记忆蒸馏/渠道/MCP 弱于 PyBot |
| **L2 工具** | `internal/tools`, 动态工具 store | 🔶 有 create_tool / 模板；沙箱弱 |
| **L2 技能** | `internal/skills` | 🔶 SKILL.md；无 marketplace |
| **L2 智能体** | `internal/agent/subagent_registry.go`, `invoke_subagent`, `run_swarm` | 🔶 有注册表+蜂群；无 DeepAgent 全套 |
| **L2 工作流** | `internal/workflow`, Flowgram | 🔶 节点子集 + checkpoint/HITL |
| **L2 应用资产** | `internal/apps`（模板/scaffold/scan） | 🔶 脚手架已对齐 create_app；无 `api.py` 子进程 |
| **L3 模式** | `internal/agent/mode.go`, `mode_policy.go`（profile × canvas） | ✅ 三 profile + 三 canvas 白名单 |
| **L3 App Matrix** | `matrix_supervisor.go`, `matrix_compose.go`, `capability` 事件 | 🔶 Supervisor + Compose；九工具编排表未全暴露 |
| **L3 Admin** | `internal/admin`, `admin_deep.go` | 🔶 骨架 |
| **L5 应用运行时** | Inner App：WebView + `invoke_inner_app` + scaffold/iterative build | 🔶 双通道有；容器/K8s 无 |

## 能力树（Capability Tree）

PyBot 另有 **`capability_tree.py`**：在 Bus 上为每个能力推断 **layer=tool|skill|agent|workflow|app** 的依赖树，用于路由提示（如 `build_app_iteratively` 优先）。

AgentGo：`internal/capability` 有 Bus + 注册，**无**同等深度的 `infer_capability_tree` 与 route_hints 注入 prompt。

## Inner App 工具链对照

| PyBot | AgentGo（当前） |
|-------|-----------------|
| `create_app` | `scaffold_inner_app` / `ScaffoldInnerApp` |
| `update_app_file` | `update_inner_app_file` |
| `verify_app` | `verify_inner_app` |
| `build_app_iteratively`（LLM 子 Agent 闭环） | `build_inner_app_iteratively`（脚手架 + 校验 + **确定性** auto-fix；**无**独立 app_generator 工具沙箱） |
| `test_app_api` / `api.py` | `CallInnerAppAPI` ping/echo/workflow_run；无 Python 执行 |

## 结论（直接回答「有几层、有没有」）

- **架构四层 L0–L3**：AgentGo **有等价职责模块**，但 **未形式化** 为 PyBot 的 `ArchitecturalLayerDescriptor` + import 守卫。  
- **五大产品概念**：AgentGo **都有对应实现或子集**，强弱见 `docs/pybot-paper-gap.md`。  
- **论文里的「L5 应用」**：AgentGo **有 Inner App + Matrix 编排**，是产品能力带，不是多出来的架构层。  
- **Capability 能力树**：AgentGo **只有 Bus 注册**，**没有** PyBot 那套自动推断 + 路由提示树。

若要进一步对齐 PyBot，优先级建议：① `build_*` 接 **带工具的子 Agent**（Eino ADK + 限定工具集）；② 暴露 Matrix 九工具 + `app_orchestrations`；③ L0 会话脊柱/上下文预算。
