# OpenBitFun (`GCWing/OpenBitFun`) 深度拆解与 `agentgo` 借鉴落地报告

> **本地克隆路径**：本机的 OpenBitFun 克隆目录（下文记作 `<OpenBitFun>`；不放 C 盘）
> **仓库地址**：[https://github.com/GCWing/OpenBitFun](https://github.com/GCWing/OpenBitFun)

---

## 1. 为什么 `OpenBitFun` 与 `agentgo` 的产品思路高度同构？

阅读 `<OpenBitFun>\README.zh-CN.md` 与 `docs/architecture/product-architecture.md` 可以看到，OpenBitFun 的核心愿景与 `agentgo` 高度一致：

| 维度 | `GCWing/OpenBitFun` | `agentgo` (本项目) |
| :--- | :--- | :--- |
| **核心产品定位** | 高性能个人 Agent 客户端（深度 Code Agent + 办公/创作 Agent + Mini App 自演进生态 + 远程控制） | 本地高性能全栈 Agent 桌面工作台（Code IDE + Workflow 编排 + Inner App 动态构建 + Memory/Channels） |
| **底层运行时** | Rust (`core` crate + Tokio) + Tauri v2 | Go 1.27 + CloudWeGo Eino v0.10 ADK + Wails v3 |
| **前端工作台** | React + SCSS + `@openbitfun/ui` 场景化工作台 | Vue 3 TSX + 模块化 CSS (`src/styles/*.css`) 三栏 IDE 壳层 |
| **微应用生态** | **Mini App**（`core.miniapp` + `Creative` 创造模式 + Worker 沙箱 + 宿主桥接） | **Inner App**（`BuildInnerAppIteratively` + `InnerAppHost` + `app_matrix` 能力） |
| **远程与跨端** | BitFun Mobile Web + 飞书/Telegram/微信机器人接入 | `ChannelsPanel`（Telegram / Discord / Feishu / WeCom / DingTalk / QQ） |

---

## 2. 已全量落地的 5 项 OpenBitFun 核心设计

### ① 四种标准 Agent Harness 协作契约（`ChatPanel.tsx` & `AppStatusBar.tsx`）
- **借鉴来源**：`<OpenBitFun>\src\shared\agent-harness\contract.json`
- **落地实现**：
  将原先分散的 `Profile (Assistant / Matrix / Admin)` × `Canvas (Focused / Balanced / Deep)` 9 种组合收敛为用户直观理解的 **4 种 Agent Harness 协作模式**：
  1. **极简 · Minimal**（`assistant + focused`）：目标明确、快速直接动手。
  2. **标准 · Standard**（`assistant + balanced`）：日常多步任务，按“规划 → 执行 → 检查”推进。
  3. **极致 · Ultimate**（`admin + deep`）：复杂任务深度拆解、全量工具协同与闭环验证。
  4. **创造 · Creative**（`app_matrix + balanced`）：专门用于构建与演进 **Inner App / Mini App** 及工作台扩展。

### ② FlowChat 探索胶囊聚合（`FlowChatTranscript.tsx`）
- **借鉴来源**：`<OpenBitFun>\src\web-ui\src\flow_chat\grouping\activityClassification.ts` & `ExploreRegion`
- **落地实现**：
  新增独立组件 `frontend/src/components/chat/FlowChatTranscript.tsx`（同时将 `ChatPanel.tsx` 行数压降至约 700 行，严格低于 900 行红线）：
  - 自动识别低风险只读探测工具（`read*`, `list*`, `search*`, `grep*`, `glob*`, `get*`, `find*`, `inspect*`, `fetch*`, `git_status`, `git_diff` 等且非报错状态）；
  - 当同一轮中出现连续 2 次及以上的只读探索调用时，自动折叠为紧凑的 **Explore 探索轨迹胶囊**（例如：`已连续探索 4 步上下文（读取 2 项 · 检索 2 次）` + 工具名徽章），支持一键展开查看明细；
  - 有副作用的修改/执行类工具（`write`, `edit`, `replace`, `bash`, `exec`, `build` 等）与报错调用保持顶层独立卡片展示。

### ③ 对话右侧回合快速跳转轨 Turn Rail（`FlowChatTranscript.tsx`）
- **借鉴来源**：`<OpenBitFun>\src\web-ui\src\flow_chat\_transcript-layout.scss` (`$flowchat-turn-rail-reserve`)
- **落地实现**：
  当会话包含 2 轮及以上问答时，在对话流右侧自动挂载粘性胶囊导航轨 `.flowchat-turn-rail`：
  - 每个用户提问自动生成带悬停摘要的回合锚点（`1`, `2`, `3`…），点击平滑滚动定位至对应轮次；
  - 支持流式状态与报错状态高亮提示，以及首尾 `↑` / `↓` 一键跳顶/跳底。

### ④ Composer 顶部吸附式审批/待办条（`ChatInputApprovalBand`）
- **借鉴来源**：`<OpenBitFun>\src\web-ui\src\flow_chat\components\ChatInputApprovalBand.tsx`
- **落地实现**：
  当会话中存在未处理的 `approval`（命令/操作审批）或 `question`（Agent 提问）时，在底部 Composer 输入框正上方固定吸附 `.chat-approval-band` 高亮条，支持直接点击「批准 / 拒绝」或「查看并回答」精准跳转，彻底解决长对话滚动后找不到审批按钮的问题。

### ⑤ Inner App 创造模式伴随悬浮窗 + 握手监控与快照回滚（`InnerAppHost.tsx`）
- **借鉴来源**：`<OpenBitFun>\src\web-ui\src\app\layout\FloatingMiniChat.tsx` & `creationRuntime.ts`
- **落地实现**：
  全面升级 `frontend/src/InnerAppHost.tsx`：
  - **运行时异常捕获与握手徽章**：在沙箱注入脚本中监听 `agentgo:inner-app-ready`、`window.onerror` 与 `unhandledrejection`，顶部状态栏实时显示 `● Ready · vN` 或 `⚠ 运行时异常`；
  - **多版本 HTML 快照栈与一键回滚**：每次成功加载或热更新自动记录快照版本（`v1`, `v2`…），若新版渲染不理想或出现报错，可点击 **「↶ 回滚上一版」** 秒级恢复；
  - **Creative 伴随演进悬浮窗（`✨ 伴随演进`）**：在应用预览右下角提供悬浮伴随窗，支持快捷芯片、运行时报错一键热修复、**「⚡ 原位热更新 Bundle」**（直接调用 `BuildInnerAppIteratively` 并热重载）以及 **「↗ 发送到主对话」**。

---

## 3. 2026-10-17 桌面实机体验反馈与下一阶段对齐计划（M11–M13）

### 实机体验暴露的三个核心问题（已在 M11 `cf83d41` 全部修复）
1. **左右主次颠倒（代码在左、对话在右 vs OpenBitFun 对话居中左、代码/画布在右）**：
   - 此前布局把 IDE 代码/工作流放在中间左，而把最核心的 Agent 对话挤在最右侧窄栏。
   - **M11 已落地**：调整为 **左导航栏 (`ActivityBar`) → 主会话流 (`ChatPanel` 居左/中，作为第一入口) → 右侧场景画布 (`Right Canvas / Workbench`：代码编辑器、Diff、内联工作流 DAG 工作室、InnerApp 预览按需在右侧展开)**，并在写文件/执行工作流时自动联动切换右侧视图与打开文件 Tab。
2. **对话流 (`FlowChat`) 视觉粗糙、卡片臃肿**：
   - 浅色模式下，编辑器主题已与全局 `light/dark` 实时同步；
   - **M11 已落地**：已处理的审批卡片自动收敛为紧凑的单行状态胶囊（`.flowchat-approval-resolved`），`ToolCallBubble` 升级为带内联参数摘要的单行胶囊卡片，只有 `pending` 态审批才展开精致操作卡。
3. **工作流编辑 (`Workflow`) 等舞台页面审美陈旧**：
   - **M11 已落地**：右侧「工作流」默认直接挂载内联 **VueFlow DAG 画布工作室**（支持 `🎨 画布编排 | 📋 列表与授权` 胶囊切换），重绘了带顶部彩色状态条与分类图标的圆角节点卡片。

---

## 4. 下一阶段深度演进路线图与全量落地记录（M12–M16：已全部完成）

结合 `agentgo` 现有 600+ Go 后端文件与 Vue 3 前端的真实代码现状，以下 **5 个核心方向（M12–M16）** 已全部完成开发、测试与打包验证（详见根目录 `plan.md`）：

### M12 · 右侧统一多场景画布标签栏（Unified Scene Canvas Tab Bar） ✅ 已落地 (`fdc52a6`)
- **解决问题**：通过左侧 `ActivityBar` 切换 `代码 / 工作流 / 扩展 / 任务 / 记忆` 时不再互斥整页替换。
- **落地实现（`frontend/src/app/SceneTabBar.tsx` + `frontend/src/stores/useLayout.ts`）**：
  - 右侧工作台顶部引入统一的 **多模态场景标签栏（SceneTabBar）**，支持多场景标签并存、实时上下文徽章（当前编辑文件名 / 微应用运行绿点 / DAG 标识）、场景跳转历史前进后退（`←` / `→`）、标签固定（`📌`）以及 **50/50 左右双景并排分屏对比（`◫ 双景分屏` + `⇄ 互换`）**。

### M13 · 对话内联 Diff 预览与单轮一键文件检查点回滚 ✅ 已落地
- **落地实现（`ToolCallBubble.tsx` + `FlowChatTranscript.tsx`）**：
  - 在文件修改类 `ToolCallBubble`（`write_file`/`edit_file`/`str_replace` 等）头部直接渲染 **`+M / -N` 行数统计徽章** 与 **「↗ 打开」** 按钮（一键在右侧代码工作台定位该文件），并在卡片下方默认展开紧凑红绿 Diff 预览块；
  - 在每轮用户提问气泡旁新增 **「↶ 回滚检查点」** 按钮，联动 `RejectAllWorkspaceReview` 一键恢复工作区磁盘文件快照并复制该轮提问。

### M14 · 子智能体（Subagent / Parallel Task）可视化嵌套执行树 ✅ 已落地
- **落地实现（`FlowChatTranscript.tsx` + `styles/chat-aui.css`）**：
  - 自动识别 `subagent` / `delegate` / `spawn_agent` / `bgtask` / `parallel_task` 类调用，渲染为带角色徽章（`🤖 子智能体` / `⚡ 并行子任务`）、任务目标摘要、实时流式进度条与可折叠子详情的 `.flowchat-subagent-card`。

### M15 · Inner App（微应用）注入标准 `window.agentgo` 宿主 SDK ✅ 已落地
- **落地实现（`InnerAppHost.tsx`）**：
  - 在沙箱注入脚本中同步暴露 `window.agentGo` 与 `window.agentgo`，内置 `fs.list` / `fs.read` / `fs.write`、`workflow.list` / `workflow.run`、`memory.search`（对接 `MemoryRecallQA`）、`agent.ask`、`ui.notify` 宿主桥接方法，带会话 `session_id` + `nonce` 校验。

### M16 · 前端分包瘦身与桌面冷启动提速（首屏体积缩减 95.4%） ✅ 已落地
- **落地实现（`vite.config.ts` + `viewRegistry.tsx` + `App.tsx`）**：
  - 对重型场景视图启用 `defineAsyncComponent` 懒加载，并通过 `manualChunks` 将 `monaco-editor`、`@vue-flow`、`lucide-vue-next` 拆分为独立缓存包；
  - 实测首屏主入口包 `dist/assets/index-*.js` 从 **4,960.40 kB (gzip 1,322.51 kB)** 降至 **226.90 kB (gzip 75.68 kB)**，首屏体积下降 **95.4%**。


