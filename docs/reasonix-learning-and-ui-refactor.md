# DeepSeek-Reasonix 经验吸收与 Wails v3 前端重构总结

> 更新日期：2026-10-15

## 一、向 `esengine/deepseek-reasonix` 学习了什么（取舍与落地）

### 1. 为什么不推倒 `agentgo` 换用 Reasonix 底壳？
- **架构完整度差距**：`agentgo` 已具备完整的 **Go 1.27 + CloudWeGo Eino + Wails v3** 五层架构（`L0 Host` → `L1 Runtime` → `L2 Harness` → `L3 Orchestration` → `L4 Capability` → `L5 Surface`）、DAG 工作流编排、Inner App 沙盒宿主、三层长期记忆（Working/Episodic/Semantic + SQLite FTS5）与人类审批门控。
- **Reasonix 的定位**：Reasonix 是一个约 1,500 行 Go 代码的极简 CLI/Web 教学型 Harness，工具集仅 6 个基础文件/Shell 工具，无工作流与沙盒应用生态。直接替换会导致 `agentgo` 80% 核心能力退化。

### 2. 从 Reasonix 吸收并落地的三项精华设计
1. **Cache-First 分层工具输出裁剪 (`internal/sessions/compile.go`)**
   - **痛点**：长会话中旧轮次的 `read_file` / `grep` / `bash` 工具输出往往长达数千至数万字符。如果每轮都原样送入 LLM，不仅挤爆 Token 预算，还会在触发硬截断时破坏消息前缀；而如果直接删除旧工具消息，又会破坏 `assistant(tool_calls)` 与 `tool(tool_call_id)` 的配对约束。
   - **落地方案**：在 `CompileOptions` 中引入 `RecentTailTurns`（默认保留最近 6 轮完整输出）与 `MaxStaleToolChars`（默认 1600 字符）。对 6 轮之前的历史 `tool` 消息，只要长度超限且不属于错误诊断输出（`looksLikeErrorToolOutput` 保持 `KeepErrors` 语义），按确定性头尾比例（前 `5/8` + 后 `3/8`）原地替换为 `...[snip: N chars omitted for prefix-cache hygiene]...`。
   - **收益**：既保持消息结构 100% 合法，又使历史前缀在多轮迭代中保持稳定，显著提升 DeepSeek / OpenAI 前缀缓存命中率。
2. **多 Provider 预设与 DeepSeek 原生支持 (`internal/bridge/app_service.go` & `SettingsPanel.tsx`)**
   - 后端新增 `DEEPSEEK_API_KEY` 环境变量感知与 `LoadDeepSeekConfig(model)` 桥接方法，支持一键切换 `deepseek-reasoner`（自动回退 `deepseek-chat`）与 `deepseek-chat`。
   - 前端设置面板新增 **Provider 预设卡片矩阵**（DeepSeek Reasoner、DeepSeek V3/Chat、Azure GPT-6 Luna、OpenAI Official、Ollama Local）。
3. **斜杠命令 (`/`) 与协作深度切换 (`ChatPanel.tsx`)**
   - 在 Composer 中支持 `/plan`、`/goal`、`/review`、`/compact`、`/new`、`/clear` 斜杠命令菜单；选择 `/plan` 或 `/goal` 时自动联动切换至 `Deep` 执行深度并注入结构化协作契约前缀。

---

## 二、前端体验差的核心根因与重构记录

### 1. 审计发现的五大体验与工程问题
1. **单文件严重膨胀，违反 900 行红线**：
   - `frontend/src/style.css` 高达 **3,380 行**；
   - `frontend/src/ChatPanel.tsx` 高达 **1,082 行**；
   - `frontend/src/panels/workspace/index.tsx` 高达 **1,030 行**；
   - `frontend/src/App.tsx` 接近 **890 行**。
2. **深色/浅色主题严重割裂 Bug**：
   - `workspace/index.tsx` 根容器硬编码了 `#18181B`，删除弹窗与打开目录弹窗硬编码了 `#1E1E22` / `#121214`；
   - `FileTree.tsx` 的搜索框与内联重命名框硬编码了 `#27272A` / `rgba(0,0,0,0.25)`；
   - `ChatPanel.tsx` 的 `@` 上下文菜单硬编码了 `#18181B`。导致切换到浅色模式时界面黑白拼贴、极其突兀。
3. **Chat 顶栏拥挤、网页感重**：
   - 顶栏塞了两个原生 `<select>` 下拉框，挤压会话标题空间；
   - 空状态仅有 3 个通用问答胶囊，缺乏面向研发/工程场景的引导；
   - `ReasoningBubble` 使用了刺眼的 `#8E44AD` 紫色，与整体青石板色桌面工具风格冲突。
4. **设置页无宽度约束、二级面板使用 Emoji 图标**：
   - `SettingsPanel` 在宽屏下 100% 拉伸，表单阅读体验差；
   - `panels/index.tsx` 的 `PanelHeader` 使用 `⚡ 📋 🛡 ⭐ 🌐 📦` Emoji，缺乏原生桌面应用质感。

### 2. 已完成的模块化拆分与视觉升级
- **样式表模块化 (`frontend/src/styles/`)**：
  - 将 3,380 行的 `style.css` 拆分为 6 个职责清晰的子样式表：`tokens-shell.css` (524 行)、`chat-aui.css` (549 行)、`rightpanel-panels.css` (802 行)、`workflow.css` (716 行)、`desktop-chrome.css` (782 行)、`reasonix-polish.css` (361 行)，由 `style.css` 统一 `@import`。
- **组件解耦与行数压降**：
  - 抽离 `frontend/src/components/chat/AUICard.tsx` (338 行)，将 `ChatPanel.tsx` 从 1,082 行降至 **579 行**；
  - 抽离 `frontend/src/panels/SettingsPanel.tsx` (297 行)，将 `App.tsx` 从 890 行降至 **741 行**；
  - 收敛 `frontend/src/panels/workspace/index.tsx` 的 ActivityBar 渲染与主题化弹窗样式，从 1,030 行降至 **865 行**。
- **桌面级交互与视觉细节打磨**：
  - 精简 `ChatPanel` 顶栏至 `40px`，新增 `Cache-First` 状态徽章；
  - 将角色模式 (`Assistant / Matrix / Admin`) 与执行深度 (`Focused / Balanced / Deep`) 下沉为 Composer 底部紧凑分段胶囊；
  - 新增 `/` 斜杠命令浮层与主题自适应的 `@` 上下文引用浮层；
  - 统一 `ReasoningBubble` 为青石板强调色，替换二级面板 Emoji 为矢量 SVG 图标。

---

## 三、下一步计划 (Next Steps)

1. **大文件继续瘦身**：
   - `frontend/src/wails.ts`（目前约 1,300 行，包含完整的浏览器预览 Mock 数据与 Wails IPC 队列）可在后续按 `wails-bridge.ts` 与 `wails-mock.ts` 拆分，使每个 TS 文件均低于 900 行。
   - `frontend/src/RightPanel.tsx`（约 908 行）可进一步将 Context Pack 预览区抽离为独立子组件。
2. **上下文 Token 水位线可视化**：
   - 将后端 `CompileProjectedView` 产出的 `Stats.EstimatedTokens` 与 `SnippedToolCount` 通过会话状态回传给前端 `AppStatusBar` 或 `ChatPanel` 顶栏，实时展示当前会话的前缀缓存裁剪次数与 Token 占用水位。
3. **桌面端真机联调验证**：
   - 在 Wails v3 桌面窗口模式下测试深浅色切换、`/plan` 与 `/goal` 协作契约流式输出以及 DeepSeek Reasoner `<think>` 思维链折叠体验。
