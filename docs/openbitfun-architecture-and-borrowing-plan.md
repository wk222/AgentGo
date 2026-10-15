# OpenBitFun (`GCWing/OpenBitFun`) 深度拆解与 `agentgo` 借鉴落地报告

> **本地克隆路径**：`D:\reference_repos\OpenBitFun`（避开空间紧张的 C 盘，存放在空间充裕的 D 盘参考仓目录）
> **仓库地址**：[https://github.com/GCWing/OpenBitFun](https://github.com/GCWing/OpenBitFun)

---

## 1. 为什么 `OpenBitFun` 与 `agentgo` 的产品思路高度同构？

阅读 `D:\reference_repos\OpenBitFun\README.zh-CN.md` 与 `docs/architecture/product-architecture.md` 可以看到，OpenBitFun 的核心愿景与 `agentgo` 高度一致：

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
- **借鉴来源**：`D:\reference_repos\OpenBitFun\src\shared\agent-harness\contract.json`
- **落地实现**：
  将原先分散的 `Profile (Assistant / Matrix / Admin)` × `Canvas (Focused / Balanced / Deep)` 9 种组合收敛为用户直观理解的 **4 种 Agent Harness 协作模式**：
  1. **极简 · Minimal**（`assistant + focused`）：目标明确、快速直接动手。
  2. **标准 · Standard**（`assistant + balanced`）：日常多步任务，按“规划 → 执行 → 检查”推进。
  3. **极致 · Ultimate**（`admin + deep`）：复杂任务深度拆解、全量工具协同与闭环验证。
  4. **创造 · Creative**（`app_matrix + balanced`）：专门用于构建与演进 **Inner App / Mini App** 及工作台扩展。

### ② FlowChat 探索胶囊聚合（`FlowChatTranscript.tsx`）
- **借鉴来源**：`D:\reference_repos\OpenBitFun\src\web-ui\src\flow_chat\grouping\activityClassification.ts` & `ExploreRegion`
- **落地实现**：
  新增独立组件 `frontend/src/components/chat/FlowChatTranscript.tsx`（同时将 `ChatPanel.tsx` 行数压降至约 700 行，严格低于 900 行红线）：
  - 自动识别低风险只读探测工具（`read*`, `list*`, `search*`, `grep*`, `glob*`, `get*`, `find*`, `inspect*`, `fetch*`, `git_status`, `git_diff` 等且非报错状态）；
  - 当同一轮中出现连续 2 次及以上的只读探索调用时，自动折叠为紧凑的 **Explore 探索轨迹胶囊**（例如：`已连续探索 4 步上下文（读取 2 项 · 检索 2 次）` + 工具名徽章），支持一键展开查看明细；
  - 有副作用的修改/执行类工具（`write`, `edit`, `replace`, `bash`, `exec`, `build` 等）与报错调用保持顶层独立卡片展示。

### ③ 对话右侧回合快速跳转轨 Turn Rail（`FlowChatTranscript.tsx`）
- **借鉴来源**：`D:\reference_repos\OpenBitFun\src\web-ui\src\flow_chat\_transcript-layout.scss` (`$flowchat-turn-rail-reserve`)
- **落地实现**：
  当会话包含 2 轮及以上问答时，在对话流右侧自动挂载粘性胶囊导航轨 `.flowchat-turn-rail`：
  - 每个用户提问自动生成带悬停摘要的回合锚点（`1`, `2`, `3`…），点击平滑滚动定位至对应轮次；
  - 支持流式状态与报错状态高亮提示，以及首尾 `↑` / `↓` 一键跳顶/跳底。

### ④ Composer 顶部吸附式审批/待办条（`ChatInputApprovalBand`）
- **借鉴来源**：`D:\reference_repos\OpenBitFun\src\web-ui\src\flow_chat\components\ChatInputApprovalBand.tsx`
- **落地实现**：
  当会话中存在未处理的 `approval`（命令/操作审批）或 `question`（Agent 提问）时，在底部 Composer 输入框正上方固定吸附 `.chat-approval-band` 高亮条，支持直接点击「批准 / 拒绝」或「查看并回答」精准跳转，彻底解决长对话滚动后找不到审批按钮的问题。

### ⑤ Inner App 创造模式伴随悬浮窗 + 握手监控与快照回滚（`InnerAppHost.tsx`）
- **借鉴来源**：`D:\reference_repos\OpenBitFun\src\web-ui\src\app\layout\FloatingMiniChat.tsx` & `creationRuntime.ts`
- **落地实现**：
  全面升级 `frontend/src/InnerAppHost.tsx`：
  - **运行时异常捕获与握手徽章**：在沙箱注入脚本中监听 `agentgo:inner-app-ready`、`window.onerror` 与 `unhandledrejection`，顶部状态栏实时显示 `● Ready · vN` 或 `⚠ 运行时异常`；
  - **多版本 HTML 快照栈与一键回滚**：每次成功加载或热更新自动记录快照版本（`v1`, `v2`…），若新版渲染不理想或出现报错，可点击 **「↶ 回滚上一版」** 秒级恢复；
  - **Creative 伴随演进悬浮窗（`✨ 伴随演进`）**：在应用预览右下角提供悬浮伴随窗，支持快捷芯片、运行时报错一键热修复、**「⚡ 原位热更新 Bundle」**（直接调用 `BuildInnerAppIteratively` 并热重载）以及 **「↗ 发送到主对话」**。
