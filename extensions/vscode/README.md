# AgentGo VS Code Extension

AgentGo VS Code 插件基于 **ACP (Agent Client Protocol)** 协议，将 AgentGo 强大的多模态 Agent 引擎、Eino 0.10 Durable Background Tasks、PlanTask 任务看板与 Code Mode 批量代码执行沙箱完整带入 VS Code。

---

## 核心能力

1. **原生 Chat Participant (`@agentgo`)**：
   - 在 VS Code 侧边栏/底部 Chat 窗口直接 `@agentgo` 呼叫。
   - 实时流式响应与 Markdown 渲染。
2. **多模式快捷指令**：
   - `@agentgo /plan`：调用 PlanTask 任务看板，自动拆解和管理开发任务。
   - `@agentgo /code`：激活 Code Mode，批量脚本处理多文件与复杂逻辑。
3. **极简单二进制依赖**：
   - VS Code 插件在后台自动拉起 `agentgo acp` 进程，通过标准 JSON-RPC 2.0 stdio 全双工通信。
   - 极低内存占用（Go 原生编译，无额外重型运行时开销）。

---

## 开发与调试

```sh
cd extensions/vscode
npm install
npm run compile
# 按 F5 即可在 VS Code Extension Host 中进行调试
```
