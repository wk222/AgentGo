# 前端重新设计（2026-10-17）

状态：**设计稿，待确认，未施工。** 范围只限 `frontend/`；后端 IPC 契约（`ipc/contract.ts`）、事件名、持久化格式不动。

## 1. 现状（实测，不是印象）

技术栈：Vue 3 + TSX（`@vitejs/plugin-vue-jsx`），Monaco，`@vue-flow`，xterm。`tsc --noEmit` 零报错，IPC 层（`ipc/`）已类型化。

| 问题 | 证据 |
|---|---|
| `App.tsx` 是上帝组件 | 869 行，同时管：主题、三个面板开关、两个拖拽宽度、项目列表、LLM 表单、会话模式、工作区审查启动、独立窗口分流、20 个内联 SVG |
| 布局状态散落 | 9 个 `localStorage` 键，各处手写读写；`activeView='chat'` 其实不是视图而是"关掉中栏"，靠 3 个布尔量互相牵制 |
| "项目"是假的 | `agentgo-projects` 只存在 localStorage，和后端工作区（`SetWorkspaceRoot`）无关；IDE 里另有一个真正的"切换工作区" |
| 死依赖 | `naive-ui` 在 `package.json` 里，源码 0 处引用 |
| 图标三套并存 | `lucide-vue-next`（7 个文件）+ `TinyIcons.tsx` + 各文件内联 `<svg>`（`App.tsx` 20 个、`ChatPanel.tsx` 10 个） |
| 杂物间文件 | `panels/index.tsx` 785 行塞 6 个面板，74 处内联样式 |
| props 钻取 | `ChatPanel` 收 ~30 个 props，全由 `App.tsx` 透传 |
| 原生弹窗 | 约 50 处 `alert/confirm/prompt`；"AI 变更保护未能启动"是阻塞式 `alert` |
| 样式分层不清 | 7 个 CSS，3 个 700–800 行；`polish.css` 靠后覆盖；`!important` 29 处 |
| 导航重复 | "代码 IDE"、项目名各出现在顶栏和侧栏；折叠按钮 3 个；设置入口 2 个；中英混用 |
| 切换视图会销毁状态 | 中栏按 `activeView` 条件渲染，Monaco 打开的标签/终端回滚条在切换后丢失（待验证） |

## 2. 目标与约束

- **目标**：一个"以对话为主轴、带 IDE 的 Agent 桌面客户端"。每个界面回答：我在哪、当前对象是什么、状态如何、下一步能做什么、什么需要我处理（审批/提问）。
- **环境**：Wails v3 + WebView2；窗口默认 1280×850；必须在 900×600 可用，1200×760 舒适。
- **不做**：换框架（保持 Vue 3 + TSX）；引入新 UI 库（反而删 `naive-ui`）；动后端；Pinia / vue-router（桌面应用没有 URL，组合式单例够用，少一个依赖）。
- **规模纪律**：单文件 ≤ 900 行；`App.tsx` 目标 < 150 行。
- **回归基线**：`tsc` 零错、`vite build` 通过、`runtime-client.test.cjs` 通过、三个窗口尺寸的截图集不退化。

## 3. 信息架构

```text
┌──────────────────────────────────────────────────────────────────────┐
│ 标题栏：[工作区切换 ▾]  当前会话标题            [命令面板] [设置] ─□✕ │  ← 唯一的工作区入口、唯一的设置入口
├───┬──────────────┬───────────────────────────────┬───────────────────┤
│ 活 │ 会话侧栏      │ 主区（按活动栏选中的视图）       │ Agent 停靠区       │
│ 动 │ (可收起)      │  代码 / 工作流 / 任务 / 记忆 /   │ 对话 + 审批 + 提问 │
│ 栏 │ 搜索+列表     │  扩展                          │ 可最大化=专注对话  │
│48px│ 新对话        │                               │                   │
├───┴──────────────┴───────────────────────────────┴───────────────────┤
│ 状态栏：连接 · 模型 · 档位/画布 · 运行状态 · 后台任务数                   │
└──────────────────────────────────────────────────────────────────────┘
```

- **Agent 不再是一个"视图"**，而是一个**区域**：三态 `docked`（右侧）/ `maximized`（占满主区，等于现在的"Agent 对话"）/ `hidden`。布局状态从 3 个互相牵制的布尔量变成一个枚举。
- **活动栏（48px 图标栏）** 取代 10 项文字导航，6 个入口：代码、工作流、任务、记忆、扩展（内置应用 / 能力中心 / Skills / Channels 作为"扩展"里的页签）、设置在标题栏。文案统一中文，英文仅作 tooltip 副标。
- **会话侧栏**承载会话列表而不是导航（会话才是高频对象）。≥1400px 默认展开，否则收起。
- **项目 = 工作区根目录**。删除假项目列表；标题栏的切换器读 `GetWorkspaceInfo`，选择走 `SetWorkspaceRoot`；"最近工作区"先放 localStorage（路径为键），后端持久化放"下一步"。
- **窄窗口（<900px）**：Agent 停靠区变覆盖抽屉，活动栏保留，会话侧栏隐藏。

## 4. 系统结构（模块与依赖）

依赖只允许 `app → features → shared` 单向；feature 之间不互相 import。

```text
src/
  app/                 壳：AppShell、ActivityBar、TitleBar、StatusBar、viewRegistry、命令与快捷键
  shared/
    ipc/               现有 ipc/（不动）
    runtime-client.ts  从仓库根 frontend-shared/ 并入，消掉 ../../../ 越界引用
    ui/                Button、IconButton、Dialog、ConfirmDialog、Toast、Splitter、Tabs、EmptyState（自写，<~300 行/个）
    icons/             统一出口，只 re-export lucide；删除 TinyIcons 与内联 SVG
    styles/            tokens.css、base.css、shell.css
  stores/              组合式单例（无新依赖）：useLayout、useChat（现 composables/useChat）、useLlmSettings、useSessionMode、useWorkspace
  features/
    chat/              ChatPanel、transcript、AUI 卡片、输入框、审批卡
    workspace/         现 panels/workspace（IDE：文件树、编辑器、终端、Git、Review）
    workflow/          现 WorkflowEditor 及 workflow-form
    tasks/  memory/  extensions/(apps、capabilities、skills、channels)  settings/
```

各模块三句话：

- **app**：负责布局与导航，决定"哪个视图在哪个区域"。不含任何业务调用。
- **stores**：负责跨视图共享的状态和对后端的调用。视图只读 store 或调用其动作，不直接 `wailsCall`（IDE 内部的纯本地调用除外）。
- **features**：每个自带 CSS 与局部状态，通过 `inject(agentBridge)` 用 `sendToAgent(text)`；不认识 `App`。

`viewRegistry` 是唯一的视图登记处：`{ id, label, icon, group, component, keepAlive }`。新增视图 = 登记一条，不再改 `App.tsx` 的 `if/else` 和 `PANEL_COMPONENTS`。

## 5. 数据结构

| 实体 | 主键 | 关键字段 | 归属 | 存储 | 生命周期 |
|---|---|---|---|---|---|
| LayoutState | 固定键 `agentgo.ui.v1` | `version, sidebarOpen, sidebarWidth, agent:'docked'\|'maximized'\|'hidden', agentWidth, view, theme` | `useLayout` | localStorage，单键 JSON，带版本迁移（读到旧 9 个键时一次性迁入并删除） | 用户偏好，可丢 |
| ViewDef | `id` | 见上 | `viewRegistry` | 代码常量 | 随版本 |
| RecentWorkspace | 规范化绝对路径 | `path, lastOpened` | `useWorkspace` | localStorage（后端持久化待定） | 可丢 |
| Session / Message | 后端 `session_id` | 现有 | `useChat` | 后端 SQLite（前端只缓存当前会话） | 后端权威 |
| SessionMode | `session_id` | `profile, canvas` | `useSessionMode` | 后端 | 后端权威 |
| LlmConfig | 单例 | `api_base, model, fallback_model, key_set` | `useLlmSettings` | 后端（key 不回显） | 后端权威 |

- 区分"用户偏好（可丢）"和"后端权威数据（前端不得私存）"。`App.tsx` 现在的 `LlmForm`、`Project` 属于前者混进了后者。
- `LayoutState` 读取失败/版本不识别 → 回退默认值，不报错、不清空其他键。

## 6. 数据流

启动：`main.ts` → `useLayout.load()`（同步，无闪烁）→ 渲染壳 → 并行 `setupWailsEvents / loadSessions / GetLLMConfig / GetWorkspaceInfo / loadHarnesses`（任一失败只在状态栏标红，不阻塞壳）。

发送消息：输入框 → `useChat.send` → `BeginWorkspaceReview`（失败：**非阻塞**横幅"变更保护未启动，修改将无法自动恢复"，不再 `alert`）→ 后端事件流 → `useChat` 归并为 `Message[]` → 视图渲染。审批/提问作为对话内联卡片，同时在标题栏和活动栏显示"待处理"徽标，避免用户在别的视图里错过。

跨视图指令：代码/记忆等视图 → `agentBridge.sendToAgent(text)` → 若 Agent 为 `hidden` 则自动 `docked`。

失败路径：
- 运行时缺失（浏览器预览）→ 走现有 mock，状态栏显示"预览模式"。
- IPC 超时 → 沿用 `withTimeout`，视图内局部错误态（说明哪个对象、数据是否已变、怎么重试），不用弹窗。
- 事件通道断开 → 状态栏"事件通道断开，重连中"，重连后 `reloadCurrentSession` 校正。
- 破坏性确认（删除会话、拒绝全部变更）→ `ConfirmDialog`；文件/目录选择仍用 Wails 原生对话框。

keep-alive：`code` 与 `workflow` 标 `keepAlive`（切走不销毁，保住 Monaco 标签、终端缓冲、画布缩放）；其余视图切走即销毁。预算：同时保活 ≤ 2 个重视图。

## 7. 视觉

方向：**克制的桌面工具**（参考 IDE 面板与系统设置），不是网页落地页。

- 一个底色 + 一层略抬升的工具栏/侧栏 + 细分隔线；一个强调色，仅用于选中/主操作/状态。
- 密度：工具栏 32–40px、列表行 30–36px、间距阶梯 4/8/12/16/24；圆角 4/6/8；阴影只给浮层。
- 令牌：`tokens.css` 一份语义令牌（背景/表面/抬升表面/三级文字/分隔线/强调/成功/警告/危险/信息），深浅两套；现有 `tokens-shell.css` 改名并去掉重复。
- CSS 分层：`tokens → base → shell → features/*.css`（与组件同目录）。**删除 `polish.css`**：把其中规则并回所属层并去掉 `!important`；目标 `!important` ≤ 5（仅第三方库覆盖）。
- 内联 `style={{}}`：布局类全部收进 class；仅保留运行时动态值（宽度、颜色变量）。
- 键盘：`Ctrl+B` 侧栏、`Ctrl+Shift+P` 命令面板（已有）、`Ctrl+L` 新会话（已有）、`Ctrl+J` 切换 Agent 区三态、`Esc` 退出最大化。

## 8. 里程碑（每步先跑基线，结束跑同一套；默认行为不变）

| # | 内容 | 验收 |
|---|---|---|
| M0 | 固化基线：`tsc`、`vite build`、`node --test`、三尺寸截图集（900×600/1200×760/1440×900，主要视图各一张）存 `docs/ui-baseline/` | 基线可一键重跑 |
| M1 | `shared/`：统一图标、`ui/` 原语（含 ConfirmDialog/Toast）、并入 runtime-client、**删除 `naive-ui`**、替换内联 SVG | 构建体积不增；视觉无变化 |
| M2 | `stores/`：`useLayout`（含旧键迁移）、`useLlmSettings`、`useSessionMode`、`useWorkspace`；`useChat` 搬家 | `App.tsx` 不再含这些逻辑；迁移用例通过 |
| M3 | 壳：`viewRegistry` + `AppShell/ActivityBar`，Agent 三态，真工作区切换器，keep-alive | 旧"Agent 对话/中栏开关"行为等价；900×600 可用 |
| M4 | 拆 `panels/index.tsx` 为 `features/*`，内联样式收进 CSS | 无文件 >900 行 |
| M5 | `ChatPanel` props → `provide/inject`；原生 alert/confirm 全部替换 | `alert(` 数 = 0，`confirm(` 仅剩 0–2（有理由的） |
| M6 | CSS 重分层，删 `polish.css` | `!important` ≤ 5；截图集与 M0 对比无回归 |
| M7 | 文档：前端结构说明；过时方案移 `docs/archive/`；改 `plan.md` | 文档与代码一致 |

M3 之前随时可停，每个里程碑独立可发布。

## 9. 风险与未验证项

- **切换视图销毁状态**（第 1 节最后一行）只读代码推断，M0 先实测再定 keep-alive 范围。
- **Wails 窗口**：自绘标题栏拖拽区与新增按钮的 `no-drag` 标记需在真机 Windows 上验；浏览器预览测不出。
- **移除 naive-ui** 前再全库确认无动态引用（`import()`、字符串组件名）。
- **默认值未验证**：侧栏 1400px 阈值、Agent 宽度吸附点 `340/420/540/680`、keep-alive 上限 2，均需用真实使用调整，先放进 `useLayout` 常量。
- 这是一次大改动：每个里程碑必须单独提交，且出问题可单独回退。

## 10. 待你确认的决策

1. **"项目"改成真实工作区**（删除假项目列表）——同意吗？
2. **活动栏（图标栏）替代现在的文字导航**，"内置应用/能力中心/Skills/Channels"并入"扩展"页签——同意吗？
3. **Agent 做成区域三态**而不是一个视图——同意吗？
4. 是否同意**删除 `naive-ui`** 并以自写原语替代？
