# `agentgo` 下一阶段全面升级施工计划（M13–M16）

> **更新日期**：2026-10-18
> **前置基线**：
> - **M0–M10**：OpenBitFun 核心架构对齐（4 种 Agent Harness 契约、Explore 探索胶囊、Turn Rail、吸附式审批条、InnerApp 伴随演进窗）。
> - **M11 (`cf83d41`)**：左对话流 + 右场景工作台布局对调、浅色/深色主题统一、已处理审批单行折叠、内联 VueFlow 工作流画布工作室。
> - **M12 (`fdc52a6`)**：右侧统一多场景标签栏 `SceneTabBar`（多标签并存、前进/后退历史、标签固定 Pin、双景 50/50 并排分屏对比）。

---

## 1. 总体目标与模块边界

本阶段（**M13–M16**）旨在打通 `agentgo` 强大的 Go 后端能力与前端桌面工作台之间的“最后一公里”，围绕 **代码变更可视化与回滚（M13）**、**子智能体层级可观测性（M14）**、**微应用宿主生产力 SDK（M15）** 以及 **桌面端首屏秒开分包优化（M16）** 四个维度完成闭环交付。

| 里程碑 | 核心模块与文件边界 | 关键产出 |
| :--- | :--- | :--- |
| **M13 · 内联 Diff 与单轮检查点回滚** | `frontend/src/features/chat/ToolCallBubble.tsx`<br>`frontend/src/features/chat/FlowChatTranscript.tsx`<br>`frontend/src/styles/desktop-chrome.css`<br>`frontend/src/styles/chat-aui.css` | 1. 文件编辑工具（`write_file`/`edit_file`/`str_replace` 等）自动解析 `+M / -N` 行数徽章与内联红绿 Diff 预览；<br>2. 点击「↗ 右侧打开」直接在右侧 `Code` 舞台定位该文件；<br>3. 每轮用户提问气泡头部新增「↶ 回滚此轮变更」检查点按钮，联动 `RejectAllWorkspaceReview` 恢复磁盘文件并回填提问。 |
| **M14 · 子智能体（Subagent）执行树卡片** | `frontend/src/features/chat/FlowChatTranscript.tsx`<br>`frontend/src/styles/chat-aui.css` | 识别 `subagent` / `delegate` / `spawn_agent` / `task` / `parallel` 类工具调用，渲染为带子任务目标、状态徽章与子输出预览的 **Subagent 嵌套执行卡**。 |
| **M15 · 微应用 `window.agentgo` 标准宿主 SDK** | `frontend/src/features/extensions/InnerAppHost.tsx` | 在沙箱注入脚本中同时提供 `window.agentGo` 与标准 `window.agentgo` 命名空间，内置 `fs.read`、`fs.write`、`fs.list`、`workflow.list`、`workflow.run`、`memory.search`、`agent.ask`、`ui.notify` 八大宿主桥接能力。 |
| **M16 · 前端分包瘦身与桌面冷启动提速** | `frontend/vite.config.ts`<br>`frontend/src/app/viewRegistry.tsx` | 1. `viewRegistry.tsx` 对重型场景视图（Monaco 代码工作台、VueFlow 工作流画布、扩展面板等）启用 `defineAsyncComponent` 按需懒加载；<br>2. `vite.config.ts` 配置 `manualChunks` 将 `monaco-editor`、`@vue-flow`、`lucide` 拆分为独立缓存 Chunk，将首屏主入口包从 **4.96 MB** 压降至轻量级。 |

---

## 2. 数据结构与数据流设计

### 2.1 M13：文件变更内联 Diff 提取流 (`ToolCallBubble`)
- **输入**：`ToolCallBubble` 接收的 `toolName: string` 与 `arguments: string`（JSON 字符串）。
- **解析规则**：
  - 匹配工具名：`/(write_file|edit_file|replace_in_file|str_replace|apply_patch|create_file)/i`。
  - 提取目标路径：`args.path || args.file_path || args.filepath || args.target_file`。
  - 提取变更内容：
    - **替换模式**（含 `old_string`/`old_text`/`search` 与 `new_string`/`new_text`/`replace`）：按行对比生成 `DiffLine[]`（`type: 'del' | 'add' | 'ctx'`），统计 `added` 与 `removed` 行数。
    - **全量写入模式**（含 `content`/`contents`）：统计 `added = lines.length`，截取前 12 行作为新增预览。
- **交互输出**：
  - 头部直接显示 `+added`（绿）与 `-removed`（红）胶囊 + `↗ 打开文件` 按钮（触发 `stageState.activeFilePath = path; useLayout().setView('code')`）。
  - 卡片中部默认展示紧凑红绿代码 Diff 块（最多显示 14 行，超出可展开全量查看）。

### 2.2 M13：单轮检查点回滚流 (`FlowChatTranscript`)
- **输入**：用户提问气泡所在的 `TurnItem`（对应 `turnNumber`、原始提问文本 `msg.content`）与当前 `sessionId`。
- **执行阶段**：
  1. 用户点击某轮提问右上角的 **「↶ 回滚检查点」**；
  2. 弹出确认框提示将恢复当前会话受保护的 AI 文件修改并重整对话；
  3. 调用 Wails 后端 `RejectAllWorkspaceReview(sessionId)` 恢复工作区磁盘文件，并通知用户可基于原提问重新发起。

### 2.3 M14：子智能体（Subagent / Task）分类与渲染流
- **识别规则**：`/(subagent|delegate|spawn_agent|run_subtask|background_task|dispatch_agent|task_agent)/i`。
- **提取字段**：子智能体角色/名称（`agent` / `role` / `subagent_type` / `name`）、子任务提示词（`prompt` / `task` / `instruction` / `query`）、执行状态（`running` / `completed` / `failed`）与返回摘要。

### 2.4 M15：`window.agentgo` 沙箱宿主桥接协议 (`InnerAppHost`)
- **通信通道**：Iframe `window.parent.postMessage({ type: 'agentgo:host-sdk', id, session_id, nonce, method, params })` $\leftrightarrow$ 宿主 `InnerAppHost.tsx` `onMessage` 处理器。
- **白名单宿主方法映射**：
  - `fs.list(relPath)` $\rightarrow$ `wailsCall('ListWorkspace', relPath)`
  - `fs.read(relPath)` $\rightarrow$ `wailsCall('ReadWorkspaceFile', relPath)`
  - `fs.write(relPath, content)` $\rightarrow$ `wailsCall('WriteWorkspaceFile', relPath, content)`（仅限工作区相对路径）
  - `workflow.list()` $\rightarrow$ `wailsCall('ListWorkflows')`
  - `workflow.run(id, input)` $\rightarrow$ `wailsCall('RunWorkflow', id, input)`
  - `memory.search(query)` $\rightarrow$ `wailsCall('SearchMemory', query, 10)`
  - `agent.send(prompt)` $\rightarrow$ 联动主对话 `sendToAgent(prompt)`

### 2.5 M16：构建分包预算与实测成果 (`vite.config.ts` & `viewRegistry.tsx` & `App.tsx`)
- **Chunk 切分策略**：
  - `vendor-monaco`：`monaco-editor` (`4,442.11 kB`)
  - `vendor-vueflow`：`@vue-flow/*` (`164.29 kB`)
  - `vendor-icons`：`lucide-vue-next` (`93.37 kB`)
  - 各场景视图通过 `defineAsyncComponent` 拆出独立异步 Chunk（`TasksPanel 2.18 kB`、`ExtensionsPanel 14.76 kB`、`InnerAppHost 14.97 kB`、`MemoryPanel 15.03 kB`、`WorkflowPanel 44.69 kB`、`CodeWorkspacePanel 438.90 kB`）。
- **实测瘦身效果**：
  - 首屏主入口 `dist/assets/index-*.js` 从 **4,960.40 kB (gzip 1,322.51 kB)** 压降至 **226.90 kB (gzip 75.68 kB)**，**首屏 JS 体积下降 95.4%**！
  - `npx tsc --noEmit` 零报错、`npm test` 8/8 全绿。

---

## 3. 已完成里程碑复盘与下一步展望（M17+）

| 阶段 | 状态 | 关键成果总结 |
| :--- | :---: | :--- |
| **M11** | ✅ 已完成 (`cf83d41`) | 左对话流 + 右场景工作台布局对调；浅色/深色编辑器主题同步；已处理审批折叠为单行胶囊；右侧内联 VueFlow 工作流画布工作室。 |
| **M12** | ✅ 已完成 (`fdc52a6`) | 右侧统一多场景标签栏 `SceneTabBar`（多标签并存、前进/后退历史、Pin 固定、双景 50/50 左右并排分屏对比）。 |
| **M13** | ✅ 已完成 | `ToolCallBubble` 文件修改工具内联 `+M / -N` 统计、红绿 Diff 预览与「↗ 打开」联动；每轮用户提问气泡旁新增「↶ 回滚检查点」。 |
| **M14** | ✅ 已完成 | `FlowChatTranscript` 新增 Subagent / 并行任务嵌套执行树卡片与实时流式进度条。 |
| **M15** | ✅ 已完成 (`9d85df5`) | `InnerAppHost` 沙箱注入标准 `window.agentgo` 宿主 SDK（`fs`、`workflow`、`memory`、`agent`、`ui`）。 |
| **M16** | ✅ 已完成 (`9d85df5`) | Vite 异步按需分包，首屏入口包从 4.96 MB 降至 226.90 kB（缩减 95.4%）。 |
| **M17** | ✅ 已完成 (`fc3e1d5`) | **仿开源 Coze Studio (`@flowgram.ai`) 工作流编排工作室全面重构**：<br>1. **根治坐标重叠与类型回退 Bug**：`nodeCatalog.ts` 新增 `normalizeCatalogType` 大小写/别名映射，彻底消除小写 `start`/`end` 误回退为 `LLM` 的问题；`flowgramAdapter.ts` 内置 `autoLayoutVueNodes` 拓扑分层自动排版与碰撞自愈机制，旧库中叠在 `(0,0)` 的双节点工作流自动恢复为 `开始 -> 大模型 LLM -> 结束` 三步流；<br>2. **Coze 富节点卡片 (`WorkflowFlowNode.tsx`)**：268px 宽卡片，含顶部主题色渐变标题栏、节点描述、运行态徽章（`运行中/完成/异常`）、结构化 `{x}` 变量胶囊行（`输入 / 模型配置 / 提示词预览 / 分支端口行 / 嵌套子画布预览 / 输出`）；<br>3. **可搜索与可拖拽节点库 (`WorkflowNodePalette.tsx`)**：支持关键词搜索、4 大分类折叠、拖拽到画布任意坐标投放，或点击自动串联并拓扑重排；<br>4. **单层 Coze 顶栏 + 底部悬浮控制坞 + 按需滑出抽屉 (`WorkflowEditor.tsx` & `FormMetaPanel.tsx`)**：合并冗余双层顶栏，新增底部毛玻璃控制坞（`+ 添加节点 | 自动排版 | 适应画布 | ▶ 试运行`），右侧未选中节点时 100% 释放画布空间，选中节点或点击「试运行」时滑出 Coze 属性检查器或试运行调试台。 |
| **M18** | ✅ 已完成 | **对话批量删除清理 + 三合一任务与线程管理中心 (`☑ 任务`) + Cursor 风格拖拽文件到对话**：<br>1. **左侧会话栏删除与批量清理 (`SessionSidebar.tsx` + `DeleteSessionsBatch`)**：每条对话悬停显示 `🗑` 秒删按钮；搜索过滤时支持一键「清理匹配 (N)」；无过滤时支持「清空其余」；右键单条会话弹出菜单（`删除此对话` / `批量删除同名对话 (N)` / `仅保留此对话（清空其余）`），瞬间清理大量 `.eval-scratch` 测试历史；<br>2. **任务与线程管理中心 (`TasksPanel.tsx` + `GetRuntimeThreadOverview` / `ToggleScheduledJob` / `TriggerScheduledJobNow`)**：顶部 4 大实时指标卡 + 3 秒自动刷新开关；三大子面板：<br>　- `⚡ 运行中线程与终端管理`：实时观测活跃引擎协程、Agent 会话阶段（思考/工具调用/流式分片/耗时/空闲秒数）与驻留 PTY 终端，支持一键 **⏹ 强制中断 (Kill)**、**⚡ 运行中插话纠偏 (Steer)**、**↗ 跳转对话** 与 **🧹 清理已结束记录**；<br>　- `⏰ 定时自动唤醒任务 (Cron)`：支持周期预设（`每30分钟` / `every 1h` / `cron:0 9 * * *` 每天 09:00 / 每周一 09:30）与自定义规则，支持 **⏸ 一键暂停/启用（防止闲置消耗 Token）**、**⚡ 立即唤醒一次** 与删除；<br>　- `📋 后台与契约任务队列`：支持手动派发不阻塞主窗口的后台异步任务（`TaskHub`）及长时契约任务（`AdminRunner` 暂停/恢复/重试/取消）；<br>3. **Cursor 风格文件/文件夹拖拽入对话 (`FileTree.tsx` + `ChatPanel.tsx` + `mentions.go`)**：右侧 Explorer 文件树节点支持直接鼠标拖拽到左侧对话窗口（带毛玻璃投放蒙层）、悬停点击 `@对话` 快捷按钮、或右键菜单选择「📎 添加到对话上下文 (@引用)」；同时支持从系统资源管理器直接拖入文件/图片；输入框上方新增 Cursor 同款 `@文件` 上下文胶囊栏（点击胶囊跳回编辑器预览，发送时自动注入文件或目录结构上下文）。 |

| **M18.1** | ✅ 已完成 | **根治「清空其余失效」与「对话内选项无法点击」两大顽疾**：<br>1. **「清空其余」全库级事务秒清 (`DeleteOtherSessions` / `DeleteSessionsByTitle` / `DeleteBatch`)**：定位到日志中 `DeleteSessionsBatch count=99/99` 后 `ListSessions count=100` 的真凶——SQLite 中存有逾千条评测残留会话，而 `ListSessions` 限制 `LIMIT 100`，前端只删当前页 99 条后下一批 100 条立刻顶上。后端 `internal/sessions/store.go` 新增 `DeleteAllExcept` 与 `DeleteByTitle` 单事务全表清理，点击「清空其余」直接清空全库所有非当前会话；<br>2. **根治「对话里的选项经常无法点击」五重阻塞**：<br>　- **UI IPC 直通免排队 (`call.ts`)**：将 `GetSessionMessages`、`ResolveApproval`、`AnswerQuestion`、`ResolveA2UIInteraction`、`DeleteOtherSessions` 等交互方法列入 `DIRECT_UI_METHODS`，绕过慢速串行 `ipcQueue` 阻塞；<br>　- **历史僵尸审批/问题自动归档 (`app_ipc.go` & `useChat.ts`)**：`GetSessionMessages` 加载历史消息时核对内存 `s.rt.pending`，进程重启后已无挂起协程的旧审批/问题自动标为 `resolved: true`，不再弹出死胡同吸顶条或将 `awaitingInteract` 设为 `true` 锁死输入框与快捷胶囊；<br>　- **修复 `ask_user` 字符串选项变数字 `"0"` 与落盘丢失 (`FlowChatTranscript.tsx` & `approval_resume.go`)**：修复 `registerPending` 漏存 `Arguments`、`mapRow` 漏映射 `data`/`interactId`、以及 `FlowChatTranscript.tsx` 与 `AUICard.tsx` 将字符串选项 `c` 误读 `c.id` 退化为下标 `"0"` 的问题；当历史问题检查点已过期时，点击选项自动回退为发送新对话消息，确保任何时候点击选项 100% 响应；<br>　- **AI 纯文本列表选项自动生成可点击胶囊 (`extractInlineChoices`)**：当 AI 未调用 `ask_user` 而在正文中列出 `1./2.` 或 `A./B.` 方案选项时，气泡底部自动提取并渲染可点击的快捷选项按钮；<br>　- **拖拽蒙层防误触 (`ChatPanel.tsx`)**：仅在外部拖入真实文件（`Files` 或 `application/agentgo-workspace-file`）时才激活 `.chat-drop-overlay` 且设为 `pointer-events: none`，杜绝对话内选字拖拽时蒙层卡死遮挡点击。 |

### 下一步可探索方向（M19+）
1. **工作流单节点逐跳流式高亮（Per-Node SSE Trace）**：在 `RunWorkflow` 执行期间通过事件流实时推送每个节点的进入、耗时与输出快照，在 Coze 富卡片上逐个点亮并支持点击节点查看单步输入/输出详情。
2. **多会话后台并发通知中心**：当用户在会话 A 工作、而会话 B 的后台工作流或长任务完成/请求审批时，在标题栏或左侧会话栏弹出跨会话轻提示并支持一键跳回。
3. **微应用（InnerApp）模板市场与一键导出独立桌面快捷方式**：支持将 Creative 模式打磨成熟的 InnerApp 一键固定到左侧主导航栏或导出分享包。

