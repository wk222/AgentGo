# 网页版对话：多会话状态、续跑与思考内容（2026-10-27）

## 现象（用真实模型复现）

1. 切换对话后，原对话的运行状态丢失（流式输出、工具气泡、待审批卡片消失）。
2. 工具执行成功后没有后续。原因：批准审批后，`ResolveApproval` 要等恢复的整轮运行结束才返回，
   经常超过前端 15 秒的默认超时，前端报错并放弃，后端其实还在继续。
3. 模型的 thinking（`reasoning`）内容没有展示。

## 改动

- `frontend/src/stores/chatRun.ts`（新）：每个会话一份 `SessionRun`（消息、发送中、审批中、流式下标、run_id 等）。
- `frontend/src/stores/useChat.ts`：
  - 切换会话只切换"当前指针"，不再取消运行；后台会话继续接收事件并按 `session_id` 路由。
  - 切回时优先显示内存里的实时状态；非运行中的会话再拉服务端消息，内容一致就保留内存版
    （这样工具气泡、思考块不会被只保存文字的服务端版本冲掉）。
  - 批准审批后若 2.5 秒内没有 `done`，用返回的 `messages` 收尾，避免界面一直转圈。
  - `AnswerQuestion` 返回的下一个暂停点（新的审批/提问）会显示出来。
  - 新增 `reasoning` 消息类型，`reasoning` 事件累积成思考块。
- `frontend/src/shared/ipc/call.ts`：`ResolveApproval` / `AnswerQuestion` 超时改为 16 分钟。
- `frontend/src/shared/runtime-client.ts`：`subscribe(null, …)` 接收所有会话的事件。
- `frontend/src/features/chat/ReasoningBubble.tsx`：流式时自动展开，结束后收起，可手动展开/收起。
- 侧边栏会话旁的状态点：运行中 / 等待审批。
- `frontend/src/shared/ipc/runtime.ts`：`localhost`/`127.0.0.1` 上先探测 `/wails/runtime.js` 是否由服务端提供
  （Vite 预览没有，网页版有），再按完整时间预算加载；用绝对地址 `import()`；加载失败不再永久缓存。
  避免网页版在慢机器上首屏超过 350ms 就落到"预览模式"。

## 验证

- 真实 真实环境（`ds`）端到端：37–71 个 `reasoning` 事件；批准后 `tool_call` / `tool_result` / 最终回答都到达；
  `ResolveApproval` 实测 17–20 秒才返回（旧的 15 秒超时必然误报）。
- 真实网页界面：发送 → 思考块（443 字，可展开）→ 审批卡 → 切到新对话（旧会话显示"等待审批"）
  → 切回 → 批准 → `code_write`、`code_view` 完成 → 最终回答，无错误，状态点消失。
- `go vet ./...`、`go test ./...`（默认与 `-tags web`）、`tsc --noEmit`、`npm test`、
  `chat-sessions` / `chat-send` 浏览器测试通过。

## 已知问题

- 思考块和工具气泡只在页面内存里，整页刷新后只剩文字、审批和提问（服务端只保存这几类）。
- `FlowChatTranscript` 里有独立的 `useChat()` 实例，`restoreTurnCheckpoint` 取到的 sessionId 为空。
- `frontend/tests/layout-spaces.browser.html`、`workbench-controls.browser.html` 对应旧版标题栏/场景标签结构
  （`.titlebar-workspace-switch`、`.scene-tab-item` 已不在组件里），与本次改动无关，需要按新布局重写。
- 手动浏览器测试时注意：IDE 的 `browser_cdp` 默认作用于"最近交互的标签页"，要显式传 `viewId`，
  否则会误操作到另一个页面（这次一度误判成"网页版落入预览模式"）。
