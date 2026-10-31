# 前端结构说明

> 对应实现：`frontend/`（Vue 3 + TSX + Vite，运行在 Wails v3 的 WebView2 里）。
> 设计依据与决策过程见 [`SYSTEM_DESIGN_2026-10-17-frontend.md`](./SYSTEM_DESIGN_2026-10-17-frontend.md)；基线数字见 [`ui-baseline/README.md`](./ui-baseline/README.md)。

## 1. 目录与依赖方向

```
frontend/src/
  main.ts / style.css      入口；style.css 只负责按顺序 @import 样式
  App.tsx                  只做窗口分流（独立 workflow / inner-app 窗口）+ <AppShell/>
  app/                     应用壳：只决定“哪个视图放在哪个区域”
    AppShell.tsx           布局、快捷键、拖拽调宽、启动时序、provideChatContext
    TitleBar / ActivityBar / SessionSidebar / WorkspaceSwitcher / StatusBar
    viewRegistry.tsx       视图注册表（新增页面只改这里）
    shell.css
  features/                业务模块；互相不直接 import（要通信走 stores）
    chat/ workspace/ workflow/ tasks/ memory/ extensions/ settings/ context/
  stores/                  跨模块状态（模块级响应式单例，没有 Pinia / vue-router）
    layoutModel.ts         纯函数：布局默认值、迁移、校验（有单测）
    useLayout / useWorkspace / useLlmSettings / useSessionMode / useChat / agentBridge
  shared/                  与业务无关的基础设施
    ipc/  wails.ts         类型化 IPC（IpcContract）+ 浏览器预览用的 mock 后端
    ui/                    notify / confirmDialog / UiHost / panelKit
    icons/ assets/ styles/ harness.ts monaco.ts runtime-client.ts
```

依赖方向只能是 `app → features → shared`，`stores` 可被 `app` 与 `features` 使用，`shared` 不得反向 import。

## 2. 几个必须知道的约定

**布局只有一份持久化状态**：localStorage 键 `agentgo.ui.v1`（带 `version`）。`loadLayout` 会把旧的零散键迁移进来；改字段必须同时改 `layoutModel.ts` 与 `scripts/layout.test.cjs`。

**Agent 区三态**：`docked`（右侧停靠）/ `maximized`（占满主区）/ `hidden`。窗口宽度 <900px 时停靠态变成覆盖层。快捷键：`Ctrl+B` 侧栏，`Ctrl+J` 循环三态。

**视图注册表**（`app/viewRegistry.tsx`）：`keepAlive: true` 的视图首次访问才挂载，之后切走只 `display:none`，不销毁（代码、工作流两个）。设置页是临时视图，不进注册表。

**工作区 = 后端真实根目录**：`useWorkspace.switchTo()` 会先跑所有 `registerSwitchGuard`（代码页用它拦截未保存标签），再调 `SetWorkspaceRoot`，成功后 `version` 加一；各视图 watch `version` 自行重载。

**对话面板不收 props**：`AppShell` 调 `provideChatContext({...})`，`ChatPanel` 用 `useChatContext()` 读（`features/chat/chatContext.ts`）。字段可以直接传 ref，内部由 `reactive` 解包。

**提示与确认**：不要用 `alert/confirm/prompt`。用 `notify.info/success/warning/error` 与 `await confirmDialog(msg, { danger })`（`shared/ui/notify.ts`，由 `UiHost` 渲染）。

**IPC**：`wailsCall('方法名', ...)` 按 `shared/ipc/contract.ts` 检查方法名、参数与返回值；新增后端方法先登记契约。动态方法名才用 `wailsCallRaw`。

## 3. 样式

加载顺序（`style.css`）：`styles/tokens-shell.css`（变量与基础）→ `styles/chat-aui.css` / `rightpanel-panels.css` / `workflow.css` / `desktop-chrome.css` → `shared/styles/ui.css` → `app/shell.css` → `features/*/*.css`。

- `!important` 全前端只允许 5 处（拖拽调宽时的 `cursor/user-select`，以及 Vue Flow 选中边的描边）。要覆盖第三方库样式，提高选择器特异性，不用 `!important`。
- 新样式放进所属 feature 的 css；不要再建“润色/补丁”类文件。
- 内联 `style={{}}` 只用于运行时动态值（宽度、CSS 变量）。**现状：面板里仍有大量内联样式，尚未清理。**

## 4. 日常命令

```powershell
cd frontend
npm run typecheck          # tsc --noEmit，必须零输出
npm test                   # runtime-client + layout，共 8 项
npx vite build             # 必须成功
npx vite --port 5199       # 浏览器预览（mock 后端，无 Wails）
```

浏览器预览测不出：自绘标题栏的拖拽/不可拖拽区域、真实文件系统、真实模型。这些要在 Wails 窗口里验。

## 5. 能力驱动交互（M8–M10）

- **单纵栏 + 内联 AI 审查条（M8）**：代码页去掉重复的 46px 纵向图标栏，改为侧栏顶部分段条（`文件 | 搜索 | Git | 大纲 | 审查(N)`）；编辑器 TabBar 下方直接展示 AI 待审变更条（查看 Diff / 全部保留 / 全部还原）；`ReviewPanel` 去掉 2s 轮询改为事件响应式更新；状态栏 `⊗ N` 报错旁提供「`✨ AI 修复`」快捷注入。
- **审批改参与中途纠偏（M8）**：审批卡片支持展开「改参 / 附言」直接修改 `overrideArgs` 与 `note` 后透传 `ResolveApproval(id, true, note, overrideArgs)`；运行中发送消息自动走 `steerSession`，并在对话流插入可见的 `⚡ [中途纠偏 Steer]` 气泡。
- **工具调用驱动主舞台联动 + 置顶任务清单（M9）**：`agentBridge.projectToolToStage` 监听 `scaffold_inner_app` / `build_inner_app_iteratively` / `invoke_inner_app` / `verify_inner_app`，自动切换主舞台到「扩展 → 内置应用」并热重载 `InnerAppHost`；应用运行时报错可在顶栏一键「`↗ 发给 Agent 修复报错`」；对话流顶部自动聚合 `TaskCreate` / `TaskUpdate` / `write_todos` 为可折叠进度条。
- **Cache-First 与记忆 X-Ray 透视器（M10）**：点击对话顶栏 `Cache-First · X-Ray` 打开右抽屉 `X-Ray` 页签（`ContextXRayView`），实时查看 `SessionSpine` 对话/工具统计、`MemoryContextPrompt` 注入预览、一键 `MemoryDistill` 提炼、活跃记忆库 `👍/👎`（`MemoryFeedback`）与可用 Skills。

## 6. 已知未完成

- 面板内联样式、内联 SVG（`TinyIcons`、`workspace/utils/icons.tsx`、ChatPanel 内 10 处）尚未统一到 `shared/icons`。
- 默认阈值（侧栏 1400px、Agent 宽度吸附点 `340/420/540/680`、keepAlive 上限 2）未经真实使用验证，集中在 `layoutModel.ts` 的 `LAYOUT_LIMITS`。
- 超过 800 行的文件：`features/workspace/index.tsx`（806）、`features/chat/ChatPanel.tsx`（794）。
