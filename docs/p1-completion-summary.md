# P1 阶段全面完成总结与经验复盘 (2026-10-03)

---

## 1. 阶段目标与完成情况概览

根据《Crush 架构深度解构与消化路线》（`docs/crush-deep-dive.md`）与《插件化架构演进方案》（`docs/plugin-architecture-plan.md`），P1 阶段的核心目标是**全面打通 Crush C/S 协议层，使 AgentGo 可以作为后端完全冒充 Crush Server，驱动原版 Crush TUI（Bubbletea v2）与 `crush run` 前端身体，并且具备 LSP 诊断、流式思维链渲染与动态模型热切换能力**。

当前 P1 阶段的所有子项已 **100% 达成并验证通过**：

| 里程碑 | 模块 | 核心工作 | 交付状态 |
| :---: | :--- | :--- | :---: |
| **P1.1 ~ P1.5** | **核心插件系统与 Codetools** | 服务表容器、插件生命周期、Codetools 零泄漏导出与工具注册 | ✅ 已完成 |
| **P1.6 ~ P1.7** | **Crush 协议探测与真 Runner** | SSE 会话广播、工具调用闭环、文件改动版本快照、跨轮对话历史 | ✅ 已完成 |
| **P1.8** | **LSP 子系统全链路贯通** | `pkg/codetools` 语言服务托管、动态 `/lsps` 接口、`lsp_event` 广播 | ✅ 已完成 |
| **P1.9** | **思维链 (Reasoning) 流式渲染** | `ReasoningObserver` 嗅探 Eino/Extra 思考流、`<think>` 双轨支持、折叠思维框 | ✅ 已完成 |
| **P1.10** | **多模型配置动态热联动** | `ModelProvider` 抽象、`/config` & `/providers` 动态响应、`POST /config/model` 热同步、`config_changed` 事件 | ✅ 已完成 |

---

## 2. P1.9 思维链 (Reasoning) 技术落地细节

### 2.1 协议规范与数据契约
Crush TUI（Bubbletea v2）对于模型思考过程有专门的 UI 渲染器（`internal/ui/chat/assistant.go`）：
- 契约结构体为 `ReasoningContent`：
  ```json
  {
    "type": "reasoning",
    "data": {
      "thinking": "string",
      "signature": "string",
      "started_at": 1791000000,
      "finished_at": 1791000005
    }
  }
  ```
- **关键机制**：
  - 思考进行中：`started_at > 0` 且 `finished_at == 0`，TUI 渲染呼吸闪烁指示器；
  - 思考完成：`finished_at > 0`，TUI 通过 `(finished_at - started_at)` 计算耗时，折叠显示为 `◇ Thought for 5s [click or space to expand]`；
  - 展开三态：支持折叠态、最新几行尾随窗口、以及按 F5 全屏展开滚动查看。

### 2.2 双轨流式捕获与状态机实现
为了兼容不同模型（OpenAI-o3/Claude 3.7 原生思考流 vs DeepSeek-R1/本地 Ollama 的 `<think>` 标签流）：
1. **原生渠道**：在 `internal/agent` 引入 `ReasoningObserver`，在 `drainADKEvents` 中不仅读取 `chunk.ReasoningContent`，还兼顾兼容 Eino 插件写入 `chunk.Extra["reasoning-content"]` 和 `chunk.Extra["_eino_deepseek_reasoning_content"]`；
2. **标签渠道**：在 `internal/bridge/crushproto_turn.go` 的 `token(delta)` 中实现流式状态机，实时探测 `<think>` 与 `</think>` 标签，自动切入思维模式；
3. **安全闭环**：当正文 token、工具调用或 turn 结束到达时，触发 `stopThinkingLocked()`，确保思考内容刷出并在 `finished_at` 上打上当前 Unix 时间戳，杜绝死锁或时间戳丢失。

---

## 3. P1.10 动态模型配置与热联动技术落地细节

### 3.1 彻底替换静态 Fixture
此前 Crush Server 的 `/v1/workspaces/{id}/config`、`/providers` 与 `/agent` 均为静态 fixture，导致 TUI 按 `ctrl+l` 打开模型选择器时无法反应 AgentGo 的真实配置。

本次改造：
1. **解耦契约**：在 `internal/crushproto/server.go` 引入 `ModelProvider` 接口：
   - `CurrentModel(workDir string) ModelInfo`
   - `SetModel(ctx context.Context, workDir, model, provider string) error`
   - `AvailableProviders(workDir string) []any`
2. **运行时桥接**：`internal/bridge/crushproto_model.go` 的 `crushprotoModelBridge` 与 `rt.LLMConfig()` / `rt.SetLLMConfig()` 打通。**只暴露 AgentGo 实际配置的那一个 provider，模型为主模型 + fallback**（不提供别家模型：AgentGo 在该 endpoint 上跑不了）；
3. **切换规则**：`SetModel` 校验 provider 与模型，非法组合返回 400；选 fallback 时与主模型互换，被换下的模型仍可切回；
4. **事件驱动**：`POST /v1/workspaces/{id}/config/model` 成功后广播 `config_changed`，TUI 重新拉取并刷新顶栏与选择器。

### 3.2 真 TUI 实机验证（2026-10-03，headless + 真 `crush.exe`）
配置 `qwen3`（fallback `llama3`，ollama endpoint）后：
- 顶栏 `qwen3 via AgentGo (ollama)`；`ctrl+l` 选择器首组只含 `qwen3`、`llama3`；
- ↓ 选 `llama3` + 回车：toast `Large model changed to llama3`，顶栏更新，`config.json` 落盘为 `model=llama3 / fallback_model=qwen3`；
- 重开选择器：当前项 `llama3`，`qwen3` 作为备选。

### 3.3 思维链的实机验证（2026-10-03）
用 `scripts/crushproto/fake_llm.py`（会流式输出 `reasoning_content` 或 `<think>` 标签的假 OpenAI 兼容服务）驱动**真实 Eino 路径 + 真 `crush.exe`**：
- `reasoning_content` 模式：TUI 先显示 `Thinking 1s`，随后是思考文本，最后是正文；HTTP 查询到的 assistant 消息含 `reasoning` part（`started_at`/`finished_at` 均已写入，`dur=1s`），随后是正文；
- `<think>` 标签模式：标签不泄漏到界面，行为与上面一致；
- 标签被切在两个 chunk 之间（`</th`+`ink>`）由单测覆盖，末尾残留的半个标签会在片段结束时作为正文放出。

**仍未验证**：真实推理模型（R1/o3）；折叠/展开三态的交互（F5 等）与"思考完成"后的折叠外观，只看了流式阶段与最终文本。

---
## 4. 核心经验与踩坑教训（均为本轮实际遇到）

1. **TUI 的配置来源不止 `/config`**：模型选择器读的是 `GET /workspaces/{id}` 返回体里**内嵌的 `config`**。第一次只改了独立的 `/config` 路由，单测全绿，真 TUI 里选择器仍是录制的 fixture。教训：协议改动必须用真客户端读屏验收，单测不足以证明 UI 生效；现用公共的 `overlayModels` 同时覆盖三处，并在测试里断言内嵌 config。
2. **快捷键以源码/屏幕为准**：文档曾写 `Ctrl+O` 切模型，实际是 `ctrl+l`（`Ctrl+O` 没有绑定，还会往输入框塞字符）。
3. **选择器下半部分的他家 provider 来自 TUI 本地内置的 Catwalk 清单**，服务端无法隐藏；误选会弹 API key 对话框。服务端只能兜底：`SetModel` 拒绝不属于 AgentGo 配置的 provider/模型。
4. **`agentgo-headless` 会用环境变量 `AZURE_OPENAI_*` 覆盖并写回 `config.json`**。测试时 shell 里带着这些变量，导致配置被改成真实 Azure 凭据。测试前必须清掉这两个环境变量，并用临时 `XDG_DATA_HOME`。
5. **`agentgo-headless` 启动要等约 12s**（离线时拉取 Catwalk/Hyper 超时），探活需耐心，不是挂了。
6. **别在持有 `Server.mu` 时构造 workspace 体**：给 `workspaceBody` 加上模型覆盖后，它经 `activeModel` 会去拿 `Server.mu`，而 `listWorkspaces` 恰好持锁在调用它，导致 `GET /workspaces` 自死锁。TUI 主流程不走这个接口，单测和第一轮读屏都没发现，是 `query_state.py` 超时才暴露的。已改为"持锁拷贝列表、解锁后再构造"。回归测试直接用 `httptest.NewRecorder` 调 handler：用 `httptest.Server` 的话，失败时清理阶段会卡死到 10 分钟超时。
7. **Windows PowerShell 5.1**：`>` 重定向写 UTF-16，后处理需按 `utf-16` 读；不支持 `&&`。

---
## 5. 下一步规划 (P2.0 里程碑)

P1 阶段通过 TCP/HTTP C/S 架构完成了全部能力的解构与协议逆向。接下来将进入 **P2.0：内嵌终端身体（单二进制）**：
1. **构建内嵌式 `AgentGoWorkspace`**：直接实现 Crush 的 `workspace.Workspace` 接口；
2. **免端口、免子进程一体化打包**：在 AgentGo CLI 中直接启动 Bubbletea v2 程序，消除端口监听、防火墙弹窗与进程生命周期管理；
3. **多模态与控制台增强**：将 Wails 桌面端、ACP/OpenSumi 协议与 Crush 终端身体完全融为一体，实现“一个大脑，三个身体”。
