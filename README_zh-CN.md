<p align="center">
  <img src="docs/UICompelte.png" alt="AgentGo Logo" height="100" style="border-radius: 8px;"/>
</p>

<h1 align="center">AgentGo</h1>

<p align="center">
  <strong>Let Agent Go!</strong>
  <br />
  <strong>本地优先的桌面智能体（Agent）运行时与客户端。</strong>
</p>

<p align="center">
  <a href="https://github.com/wk222/AgentGo">
    <img src="https://img.shields.io/badge/Wails-v3-red?logo=go&style=flat-square" alt="Wails v3"/>
  </a>
  <a href="https://github.com/wk222/AgentGo">
    <img src="https://img.shields.io/github/go-mod/go-version/wk222/AgentGo?style=flat-square&logo=go" alt="Go Version"/>
  </a>
  <a href="https://github.com/wk222/AgentGo">
    <img src="https://img.shields.io/badge/License-Apache_2.0-blue?style=flat-square" alt="License"/>
  </a>
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README_zh-CN.md">中文说明</a>
</p>

---

## 🚀 简介

AgentGo 是一款本地优先的桌面智能体（Agent）运行时与客户端。它深度整合了 Go 语言、字节跳动 Eino 框架的 Graph/ADK 能力，以及 Wails v3 原生桌面容器，并提供了包含工作区文件上下文、长期记忆系统、工具治理以及微应用（Inner App）生成的完整技术底座。

借助简洁现代的视觉界面与底层架构，AgentGo 能够让智能体突破传统对话交互的局限，在本地环境里实现多智能体工作流与微应用的持续沉淀、可视化编排与执行。

### 🖥️ 界面预览

<p align="center">
  <img src="docs/workflow_editor.png" alt="可视化工作流" width="48%"/>
  <img src="docs/inner_app.png" alt="内置应用沙盒" width="48%"/>
</p>

---

## 🌟 核心功能

*   **🎨 可视化工作流设计器 (Flowgram → Compose)**：在画布上直观编排复杂的有向无环图 (DAG) 工作流。支持循环节点、分支规则、并行执行、Checkpoint 恢复及人工介入 (HITL) 审批。
*   **🔌 动态能力总线 (CapabilityBus)**：统一的能力资产索引注册表，在应用启动时自动扫描、热登记并向系统暴露 Tools、Skills、Workflows、Inner Apps 及子智能体。
*   **🔗 Eino ADK 编排**：底层深度集成字节跳动 Eino 框架，保证智能体、工作流、记忆召回和工具执行的图层运行安全与结构化控制，而非简单的一次性调用链。
*   **🧠 高级记忆控制台**：支持认知冲突消解的 **Truth Queue** (真相队列)、可视化 1-hop 记忆链的 **Graph View** (图谱视图) 以及能够在请求 LLM 前查看精确 Token/字节占比的 **Injection Preview** (注入预览)。
*   **🛡️ 治理与策略管道**：工具、Bash、数据库调用和工作流节点均共享一套拦截治理中间件，支持自愈校验、审计追踪和并发限制（例如 Token 限制或权限熔断）。
*   **📦 Inner App 沙盒**：通过内置的 App Builder 智能体，使用自然语言迭代构建、校验、自动修复并运行可视化桌面微应用 (Inner Apps)。

---

## 🤖 AI 原生独特优势

#### 1. AI 创建 Inner App (AI 原生桌面应用沙盒)
*   **语言即应用**：用户只需以自然语言提出需求，系统将自动构建包含 UI HTML 骨架、样式和模拟交互逻辑的桌面微应用。
*   **确定性自愈**：集成本地沙盒校验机制。若检测到编译报错或布局缺失，系统会自动将错误反馈给 `app_builder` 智能体，触发自动修复闭环，直至通过校验。

#### 2. 可编辑的 Workflow-as-a-Tool (工作流即工具)
*   **自然语言编排**：用户使用自然语言描述复杂的多步逻辑，系统自动将其编译为符合 Eino 规范的 DAG 可视化工作流。
*   **可视化调整**：生成的流程完全透明且支持二次编辑。用户可通过独立的画布直观调整节点、微调 Prompt 模板及编辑分支逻辑。
*   **工具化封装**：工作流发布后可一键封装为标准 **Tool** 登记至能力总线，供主智能体或其他工作流直接调用。

#### 3. Inner App 转化为工具 (Inner App as Tool)
*   **无缝继承**：微应用注册后，其核心交互或服务能力会自动封装为标准的 `invoke_inner_app` 工具并暴露至能力总线。
*   **协同编排**：主智能体或工作流可像调用常规 API 一样调度该微应用的界面或业务逻辑，轻松实现深层嵌套的多智能体协同。

---

## 🧱 架构分层

AgentGo 遵循严格的 5 层架构设计 (L0 至 L4)，以确保模块解耦与代码可维护性：

```text
L4 消费层 (Wails IPC、独立桌面窗口、Web/SSE 网关)
  └─ L3 产品模式 (Matrix 编排、Admin 规划、会话状态机)
      └─ L2 资产域 (工具 Tools、技能 Skills、工作流 Workflows、应用 Apps)
          └─ L1 核心系统 (治理规则、记忆注册表、能力总线 CapabilityBus)
              └─ L0 基础运行时 (DB 存储、工作区沙箱、应用日志)
```

---

## 📚 文档指南

- [架构说明](docs/ARCHITECTURE.md)
- [内部文档指引](docs/README.md)
- [Matrix 编排引擎](docs/matrix-orchestration.md)
- [与 PyBot 层级对比](docs/pybot-layer-map.md)
- [可复现构建指南](docs/build-reproducible.md)

## 📄 许可证

本项目开源协议见 [LICENSE](LICENSE) (Apache 2.0)。
