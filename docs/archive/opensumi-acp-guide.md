# AgentGo + OpenSumi ACP 集成实战指南

本文档介绍如何基于 OpenSumi 的 **Agent Client Protocol (ACP) 工作流（PR #4767）** 与 **AgentGo** 进行前后端解耦与工业级整合。

---

## 1. 架构总览

```
┌──────────────────────────────────────────────────────────────────┐
│                   OpenSumi 桌面端 / Web IDE                      │
│                                                                  │
│  ┌─────────────────────────┐      ┌───────────────────────────┐  │
│  │ Agent Task List (任务流) │      │ Pinned Editor Tabs (编辑器)│  │
│  │ (多会话/状态/未读标记)   │      │ (Monaco + 行内 Diff 审查) │  │
│  └────────────┬────────────┘      └─────────────┬─────────────┘  │
│               │                                 │                │
│  ┌────────────▼─────────────────────────────────▼─────────────┐  │
│  │ ACP Chat View (ACP 对话主视图 / 权限确认弹窗 / 工具卡片)    │  │
│  └──────────────────────────────┬─────────────────────────────┘  │
└─────────────────────────────────┼────────────────────────────────┘
                                  │ 标准 ACP 协议 (stdio / JSON-RPC 2.0)
                                  ▼
┌──────────────────────────────────────────────────────────────────┐
│              AgentGo 引擎 (agentgo.exe --acp)                    │
│                                                                  │
│  ├── Eino 智能体流水线 (v0.10.0-alpha.32)                       │
│  ├── 实验静默后台监控 (job_monitor 自动唤醒与任务上报)          │
│  ├── 思考过程流式分发 (<think> 标签 -> ACP thought 事件)        │
│  ├── 架构师 / 开发者 / 咨询 动态模式切换 (modes)                │
│  ├── 记忆图谱与知识沉淀 (memory / HTML 可视化)                  │
│  └── 治理安全拦截与工具权限审批 (Approval Queue)                │
└──────────────────────────────────────────────────────────────────┘
```

---

## 2. AgentGo ACP 服务端接口契约

`agentgo.exe --acp` 启动后，标准输入（`stdin`）接收 JSON-RPC 请求，标准输出（`stdout`）纯净输出 JSON-RPC 响应，日志统一重定向至 `stderr`，确保与任何标准 ACP 客户端兼容。

### 支持的核心方法

| 方法名 (支持标准与路径别名) | 方向 | 职责 |
| :--- | :---: | :--- |
| `initialize` | Client -> Agent | 握手与能力协商（协议版本、会话能力、加载会话支持） |
| `newSession` / `session/new` | Client -> Agent | 创建新会话，返回会话 ID、可选模式列表（Modes）与模型选项（ConfigOptions） |
| `loadSession` / `session/load` | Client -> Agent | 恢复已有会话 |
| `prompt` / `session/prompt` | Client -> Agent | 发送用户提示词，开始一轮智能体推理 |
| `sessionUpdate` | Agent -> Client (通知) | 流式推送思考片段（`thought`）、正文（`agent_message_chunk`）、工具卡片（`tool_call`） |
| `setSessionMode` / `session/setMode` | Client -> Agent | 动态切换模式（`architect`、`coder`、`ask`） |
| `cancel` / `session/cancel` | Client -> Agent | 中断当前会话正在执行的推理流 |
| `closeSession` / `session/close` | Client -> Agent | 释放会话资源与状态 |

---

## 3. 命令行验证与联调测试

### 测试 1：测试握手初始化
```powershell
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}' | .\agentgo.exe --acp
```
**预期输出**：
```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"mcpCapabilities":{"acp":true,"http":true},"sessionCapabilities":{"close":{}}},"agentInfo":{"name":"AgentGo","version":"0.10.0"},"authMethods":[]}}
```

### 测试 2：测试会话创建与模式枚举
```powershell
echo '{"jsonrpc":"2.0","id":2,"method":"newSession","params":{"sessionId":"test-1"}}' | .\agentgo.exe --acp
```
**预期输出**：
```json
{"jsonrpc":"2.0","id":2,"result":{"sessionId":"test-1","modes":{"currentModeId":"coder","availableModes":[{"id":"architect","name":"Architect (系统架构师)","description":"系统设计、范式推演与设计审查"},{"id":"coder","name":"Coder (开发工程师)","description":"编写实现代码、执行测试并修复缺陷"},{"id":"ask","name":"Ask (专家答疑)","description":"代码库解读、问答与只读建议"}]},"configOptions":[{"id":"model","name":"Model Selection","type":"select","category":"model","currentValue":"default","options":[{"value":"default","name":"AgentGo Active"},{"value":"deepseek-chat","name":"DeepSeek V3"},{"value":"deepseek-reasoner","name":"DeepSeek R1 (深度思考)"},{"value":"claude-sonnet-4-5","name":"Claude 3.7 Sonnet"}]}]}}
```

---

## 4. OpenSumi 客户端接入配置

在 OpenSumi 中，将 AgentGo 登记入 ACP Agent Catalog（例如配置在 `opensumi-acp.json` 或 `settings.json` 中）：

```json
{
  "ai.native.acp.agents": [
    {
      "id": "agentgo",
      "name": "AgentGo",
      "command": "C:\\Users\\user\\Documents\\GitHub\\agentgo\\agentgo.exe",
      "args": ["--acp"],
      "env": {
        "AGENTGO_MODE_PROFILE": "assistant"
      }
    }
  ]
}
```

在 OpenSumi 启动后，OpenSumi 会直接拉起 `agentgo.exe --acp`，自动在左侧 **Agent Task List** 展示 AgentGo，用户即可直接开启全自动的 Agentic 编程工作流。
