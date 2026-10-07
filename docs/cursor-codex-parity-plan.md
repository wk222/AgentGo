# AgentGo 对标 Cursor / Codex 能力矩阵与补强路线图

日期：2026-10-07  
状态：持续推进中（已完成交互式 PTY 终端、@ 上下文展开服务、Monaco 快捷内联编辑、状态栏诊断指示、GUI 多轮持久化历史、工具原子对齐）

---

## 一、本次已补全的核心差距

| 序号 | 模块 / 能力 | 此前现状（落后点） | 本次补强（已落实） | 验证结果 |
| :--- | :--- | :--- | :--- | :--- |
| **1** | **交互式终端 (Terminal)** | 伪终端（单行 input 调 `powershell -Command` 同步等 30 秒，无法交互，无 ANSI，无实时流，无中断） | **基于 `@xterm/xterm` + `@xterm/addon-fit` 重构**：<br>1. 对接底层 `go-pty` ConPTY 真实流式输出；<br>2. 支持多标签页（切换、新建、关闭）；<br>3. 支持动态拖拽调整抽屉高度与最大化切换；<br>4. 支持 `^C` 中断与预设快捷命令；<br>5. 完善前端 preview/mock 交互。 | 前端构建 `build` 成功，类型检查 0 错误 |
| **2** | **上下文引用与展开 (@ Mentions & Expander)** | 仅纯文本输入，附件按钮处于禁用状态，无上下文靶向引入；后端无上下文展开 | **Composer 上下文选择浮层 + 后端 ContextExpander**：<br>1. 前端输入 `@` 自动触发悬浮选择器，支持 `@Codebase`、`@Git`、`@Terminal`、`@Problems` 及项目文件模糊提示；<br>2. 后端新增 `context_expander.go`，在送入模型前自动解析抓取 `git status/diff`、活动终端缓冲、LSP 诊断和目标文件内容，权威注入 prompt。 | `TestExpandUserMentions` 单元测试通过，前端 build 成功 |
| **3** | **Monaco 内联编辑 (Ctrl+K) 快捷交互** | 只能鼠标点击「应用修改」，无快捷键流转，无键盘操作提示 | **键盘快捷键流转**：<br>1. 支持 `Ctrl+Enter`（或 `Cmd+Enter`）直接接受修改并推入 Undo 栈；<br>2. 支持 `Esc` 一键取消退回；<br>3. 浮层内直观标注操作指引，对齐 Cursor 肌肉记忆。 | 前端类型检查 0 错误 |
| **4** | **工作区诊断指示 (Problems Indicator)** | 编辑器底部状态栏缺少错误/警告统计，无法感知语言服务状态 | **状态栏增加 `⊗ / ⚠` 诊断计数与后端接口**：<br>1. 后端新增 `WorkspaceDiagnostics()` Wails 绑定，拉取活动 LSP 服务错误；<br>2. 状态栏提供实时计数显示与点击手动刷新。 | 前端构建成功，后端 build 成功 |
| **5** | **GUI 对话历史连续性** | 仅 TUI (Crush) 接入了 `WithHistory`，GUI `SendMessageStream` 与 `SendMessageWithSession` 未将数据库历史送入上下文，多轮对话丢失上文 | **统一 `sessionHistory` 载入**：<br>在 `stream.go` 的 `runStream` 与 `app_ipc.go` 的 `SendMessageWithSession` 追加当前提问前，先加载持久化历史并通过 `agent.WithHistory` 注入 `ctx`，与 TUI 保持同等记忆连续性。 | `internal/bridge` 测试全部通过 |
| **6** | **上下文裁剪与工具对齐 (Codex 核心差距)** | `compile.go` 简单按条数截断 `active`，极易造成 Assistant 工具调用与其 Tool 响应被腰斩，触发 LLM API 400 报错 | **原子工具组归一化 (`pruneAndNormalizeActive`)**：<br>实现类似 Codex `normalize.rs` 的闭环校验：过滤孤立的 tool 结果，剔除无对应响应的 tool_call，保证调用与返回成对投递。 | `TestCompileProjectedView_AtomicToolPairNormalization` PASS |
| **7** | **长任务中途纠偏与输入队列 (Mid-turn Steering & Inbox)** | 智能体在多步执行工具期间，输入框直接禁用，无法干预模型偏差 | **运行中实时纠偏通道**：<br>1. 后端 `RunControl` 实现 `AddSteer` 与 `DrainSteers`，在 `runner.go` 每次工具调用循环后优先检查插话队列；<br>2. 暴露 `SteerSession` 接口；<br>3. 前端在任务运行状态下允许键入并一键发送「⚡ 纠偏」文本。 | `TestRunControl_SteerQueue` PASS，前端 build 成功 |

---

## 二、当前相比 Cursor / Codex 仍存在的差距与下一步施工排期

### 1. Monaco 原生行内 Diff 比对 (Inline ZoneWidget Diff)
* **Cursor 体验**：按 `Ctrl+K` 生成代码后，直接在编辑器原代码下方插入绿色/红色行内对比块，而不是浮动小面板。
* **下一步规划**：基于 Monaco ViewZone / ZoneWidget 将生成的补丁原位展开为行内对比条目。

### 2. Multi-file Fast Edit (多文件批量投递修改)
* **Cursor Composer 体验**：一次对话提出需求，智能体规划并同时修改多个文件，在界面上呈现文件折叠卡片与统一 Apply / Discard 按钮。
* **下一步规划**：在 `internal/codetools` 中完善多文件编辑事务与前端统一 Review 面板。

---

## 三、经验总结与避坑要点

1. **终端 PTY 必须遵循事件驱动**：
   - 终端是高频双向流，不能用同步 HTTP/IPC 轮询，必须依托后端 `ProcessSupervisor` 的 output buffer 与 Wails 事件流（`terminal:data`）。
   - 前端必须在容器尺寸改变时调用 `fitAddon.fit()` 并通知后端 `TerminalResize`，否则全屏 CLI 工具（如 vim、top、交互式安装向导）排版会错乱。
2. **上下文裁剪严禁孤立 Tool 消息**：
   - LLM（尤其是严格遵从 OpenAI 协议的模型，如 Azure OpenAI、Claude 3.7/3.8）对消息序列有极高要求，一旦出现没有前置 `tool_calls` 的 `role: tool`，或者有 `tool_calls` 却缺失响应，会发生不可逆的 400 失败。原子成对检查必须成为基线纪律。
