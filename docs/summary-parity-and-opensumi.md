# AgentGo 对标 Cursor/Codex 阶段性经验总结与下一步规划

日期：2026-10-07  
状态：已完成核心落后点补齐并通过全量测试，准备提交入库

---

## 一、本次核心落地特性总览

| 模块 | 对标对象 | 核心改动与技术方案 | 验证指标 |
| :--- | :--- | :--- | :--- |
| **交互式终端 (Terminal)** | Cursor Terminal | 引入 `@xterm/xterm` + `@xterm/addon-fit`，对接底层 `go-pty` ConPTY 双向实时流；支持多 Tab 切换、高度鼠标无级拖拽、全屏切换与 ANSI 彩色渲染。 | 前端构建通过，双向流式响应正常 |
| **上下文引用展开 (@ Mentions)** | Cursor Composer | 前端实现 `@` 浮层模糊补全；后端新增 `context_expander.go`，在送入模型前自动解析并提取真实 Git diff/status、终端缓冲、LSP 诊断与文件源码。 | `TestExpandUserMentions` PASS |
| **持久化历史记忆注入** | Codex 多轮对话 | 补齐 GUI 桌面端 `runStream` 与 `SendMessageWithSession` 遗漏的 `agent.WithHistory` 上下文载入链路，与 TUI 保持同等记忆连续性。 | `internal/bridge` 测试 PASS |
| **上下文裁剪与工具对齐** | Codex 鲁棒性 | 在 `sessions/compile.go` 中实现 `pruneAndNormalizeActive` 与 `normalizeToolPairs`，杜绝截断时孤立 tool 消息导致的 LLM 400 崩溃。 | `TestCompileProjectedView_AtomicToolPairNormalization` PASS |
| **长任务中途纠偏 (Mid-turn Steer)** | Codex `turn/steer` | 后端实现 `RunControl` 插话队列并在 `runner.go` 循环中主动吸纳纠偏；前端支持任务运行中键入并一键发送「⚡ 纠偏」文本。 | `TestRunControl_SteerQueue` PASS |
| **Monaco 快捷内联编辑** | Cursor `Ctrl+K` | 支持 `Ctrl+Enter` 接受修改并入 Undo 栈，`Esc` 一键取消退出，支持键盘全流程流转。 | 前端构建与快捷键绑定验证无误 |
| **工作区诊断指示** | Cursor 底部状态栏 | 后端暴露 `WorkspaceDiagnostics` 接口，前端状态栏提供实时 `⊗ / ⚠` 计数显示与点击刷新。 | 前端类型检查无误 |

---

## 二、OpenSumi 本地参考与定位说明

* **核心定位澄清**：
  - AgentGo 桌面端主 UI 确立为自研的轻量级纯原生前端（Vite 6 + Vue 3.5 TSX + Monaco Editor + Naive UI + `@xterm/xterm`），保障极速冷启动、单二进制自包含打包（Windows 资源直接内嵌）以及与 Go 后端通信的零冗余。
  - OpenSumi 在本项目中定位于**标准化协议与外部宿主参考**，通过标准 JSON-RPC 2.0 ACP 协议（`agentgo --acp`）对外无缝兼容。
* **本地留存处理**：
  - 包含 OpenSumi 完整构建、数十个 `@opensumi/*` 依赖与示例代码的工程已统一归置至本地目录 `frontend-opensumi/`。
  - 在 `.gitignore` 中显式忽略 `frontend-opensumi/`、`opensumi-reference/` 与相关构建脚本，**确保 OpenSumi 目录完整留存本地磁盘供参考，绝不污染或提交至远程 GitHub 仓库**。

---

## 三、排错复盘与踩坑记录

1. **Windows ConPTY 双向流断开排查**：
   - 现象：前端打开终端抽屉后偶现黑屏无响应。
   - 根因：终端初始化时若未触发容器 resize，ConPTY 初始行列与前端 DOM 尺寸脱节；此外子进程退出时事件未正确传播至前端 Tab 状态。
   - 解决：在 TerminalTab 组件挂载阶段增加基于 `requestAnimationFrame` 的首帧 `fit()` 强制对齐，并由后端 `ProcessSupervisor` 在进程退出时广播 `terminal:exit` 事件通知前端标记已退出状态。
2. **上下文裁剪中工具消息与调用的原子配对**：
   - 现象：长上下文多轮对话且模型触发较多工具时，偶发 400 Bad Request（`An assistant message with 'tool_calls' must be followed by tool messages`）。
   - 根因：传统滑动窗口按消息条数粗暴裁剪，刚好切掉了前置 assistant 的 `tool_calls` 或截断了其对应的 `role: tool` 响应。
   - 解决：借鉴 Codex `normalize.rs` 的双向配对算法，在每轮上下文投递前扫描并移除非成对出现的孤立项，确保消息流结构合规。
3. **PowerShell 5.1 命令自检纪律**：
   - 严格避免在 Windows 下使用 `&&` 串联命令，避免 heredoc 重定向，采用 Python 脚本与专用工具精准读写，防止出现参数吃引号或 UTF-16 编码损坏。

---

## 四、本地 Azure GPT-6 Luna 密钥适配说明

1. **凭证自动识别**：
   - AgentGo 现已支持从宿主环境变量自动探测 `AZURE_OPENAI_API_KEY` 与 `AZURE_OPENAI_API_ENDPOINT`。
   - 自动映射基地址至 `{endpoint}/openai/v1/`，默认选用 `gpt-6-luna`。
   - 启动时自动将 `AGENTGO_REASONING_EFFORT` 设为 `none`，杜绝 Azure Luna 推理部署在 `/chat/completions` 接口上拒绝工具调用的报错。
2. **测试与使用方式**：
   - **方式一（命令行免配置直启）**：
     ```powershell
     .\bin\agentgo.exe --luna
     ```
     后台自动加载 Luna 密钥与端点，终端打印：`[AgentGo] 已启用本地 Azure Luna: model=gpt-6-luna, api_base=...`。
   - **方式二（GUI 桌面设置界面一键切换）**：
     点击左侧或活动栏「设置」，LLM 配置卡片顶部若检测到环境变量，会自动显示 `[⚡ 切换至本地 Azure Luna]`，点击即可一键填入并自动完成测试连接。
   - **已实测验证**：
     使用 `TestAzureLunaLive` 经由真实端点探活，返回 `HTTP 200`，模型列表中成功识别到 `gpt-6-luna` 与 `gpt-5.6-luna`。

---

## 五、下一步演进规划（Next Steps）

1. **Monaco 行内 Diff 审查（Inline ZoneWidget Diff）**：
   - 当前内联编辑为浮层模式，下一步将其升级为原生 ViewZone 绿色/红色行内 Diff 对比卡片，支持按块（Hunk）局部接受/拒绝。
2. **多文件批量修改流（Multi-file Fast Edit & Review）**：
   - 在 Composer 界面中支持大模型一次性规划并修改多个工作区文件，并在前端提供统一的 Changes 变更集折叠卡片，支持一键「全部应用」或「逐文件审查」。
3. **LSP 深度诊断悬浮与 CodeAction 联调**：
   - 将工作区 `WorkspaceDiagnostics` 深度注入 Monaco Editor 的 Marker 体系，实现代码下方的红黄波浪线与「Quick Fix」快速修复悬浮窗。
