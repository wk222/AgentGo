# AgentGo 与 Codex 开源执行链对照

日期：2026-10-07。范围：当前源码审计，不等同于完整 UI 或真实模型验收。

参考仓库已浅克隆到 `C:/Users/wgk/Documents/GitHub/codex`，来源 https://github.com/openai/codex ，提交 `406eb53c074a29bd65aa7a8bd21d574234faf315`。只阅读源码，没有安装依赖或编译 Codex。

## 判断

AgentGo 已有真实插件装配、工作区生命周期治理、运行日志和跨界面审批中继。旧计划 F0–F6 的已交付工作继续保留。

当前主要差距是运行契约没有覆盖所有主要入口。插件数量、类型和依赖排序的增加，不会自动补齐对话连续性、执行事件、进程交互和任务恢复。优先完成一条纵向编码链，再增加生态功能。

## 1. GUI 仍有同步回答适配

证据：`frontend/src/browser/render-app.ts` 的 `aiBackServiceInstance.requestStream` 调用同步 `SendMessage`，等待最终结果后才向 stream 发送整段文本。该适配器实际被注册为 AI 后端服务。

`bridge.AppService.SendMessage` 使用固定 session ID `desktop`，直接调用 `sendMessageCore`；它没有经过 `runEngineTurn` 的运行日志包装。另一条旧入口 `runStream` 也直接调用 `sendMessageCore`，自行处理持久化和 chat 事件。

影响：这些 GUI 路径不能获得与 TUI 相同的运行 ID、事件流和日志语义。该结论是源码路径核查，本轮没有打开原生窗口确认所有界面入口。

Codex 对照：`codex-rs/app-server-protocol/src/protocol/common.rs` 定义 `turn/start`、`turn/steer`、`turn/interrupt`；`protocol/item_builders.rs` 和 `protocol/event_mapping.rs` 将核心 item 生命周期投影给客户端。

优先修复：OpenSumi 保留编辑器和工作台，AI 后端接统一 Start/Subscribe/Decide/Cancel 契约。旧 SendMessage/SendMessageStream 改为兼容转发，不独立执行和保存。

## 2. 持久化日志尚未统一成为模型历史来源

证据：`internal/agent/history.go` 的 runMessages 只读取上下文中的 WithHistory，再追加本次输入。生产检索中，明确加载 WithHistory 的入口是 `bridge/crushproto_runner.go`。

`bridge/crushproto_history.go` 只重放 user/assistant 文本，排除工具交通、审批和问题行，限制为 24 条消息和 24000 字符。SessionSpine 虽读取数据库，但相关代码只为已有 state 中的 user 消息补图片，并未将全部历史追加到 state；对 `desktop` 还跳过该读取。

影响：显示完整历史、记录运行事实、实际给模型完整历史是三件不同的事。当前不同入口仍可能获得不同上下文；跨回合工具证据无法通过这条文本历史路径原样传递。

Codex 对照：`core/src/context_manager/history.rs` 的 `for_prompt` / `for_prompt_with_annotations` 使用规范化的历史；`normalize.rs` 处理缺失工具输出、孤立输出及对应调用的删除。

优先修复：统一 ContextCompiler 从权威 session 事实生成模型输入。客户端不负责决定历史；保留用户约束、工具调用/结果配对、待办状态及附件身份。

## 3. 上下文裁剪与预算检查没有形成闭环

证据：`sessions/compile.go` 的 MaxActiveTurns 实际按消息条数截尾，可能截开调用/结果组。`agent/context_inspector.go` 超预算时记录 TruncationLog，但该检查本身不裁剪；SessionSpine 随后仍 Render 原 view。估算不包含完整工具 schema 和调用参数。

当前已有 episodic compressor、工具结果 pruning 和 patchtoolcalls，不能把它们当成不存在；问题是缺少统一的预算执行与保留约束。成对裁剪问题是源码可达风险，本轮没有提交新复现测试。

Codex 对照：`context_manager/history.rs` 删除最早 item 时关联删除对应项；`compact.rs`、`compact_remote_v2.rs`、`compact_token_budget.rs` 提供不同压缩实现。

优先修复：预算计算覆盖实际请求；按完整调用组和回合处理历史；先生成有保留约束的摘要，再替换窗口。报告记录实际删改，不能只记录拟删数量。

## 4. 审批决定统一了，恢复的完整事件仍未统一

证据：`bridge/approval_resume.go` 统一决定和续跑，但直接通过 Runtime.resumeAgent 调用 Runner；`agent/adk_run.go` 的 ResumeInterrupt 向 resumeADKAgent 传入 nil 文本 emitter。旧计划也登记了续跑段内部逐步事件缺失。

影响：审批后能完成任务，却无法保证界面获得与首段同样的工具、推理、文本和用量事件。最终结果中继弥补了可见性的一部分，未替代完整执行流。

优先修复：初次执行和 checkpoint 恢复使用同一个绑定 run/session 的事件接收器、日志提交路径和取消管理。区分暂停段结束与任务最终结束。

## 5. 长任务和中途输入需要执行层支持

证据：本轮没有检索到统一 steer/inbox 入口。已有取消、后台协调、job_monitor 和 IDE 终端服务；但核查的 `terminal.Run`、coderuntime.Execute、execute_bash 都是等待 cmd.Run 完成的调用，不能据此认为整个仓库没有 PTY。

Codex 对照：`core/src/tasks/regular.rs` 在任务循环检查 pending input；`core/src/session/turn.rs` 结合模型 follow-up 和 pending input 判断是否继续。`core/src/unified_exec` 提供进程 handle、输出缓冲、stdin、超时与取消，并通过共享工具编排处理策略。

优先修复：把现有终端/进程服务变为 agent 可用的统一能力，支持启动、等待、输入和结束；为运行中的用户纠正建立输入队列和采纳回执。

## 实施顺序与验收

1. GUI 真流式 + 真实 session ID + 全入口经过统一运行服务。
2. 统一历史编译 + 工具证据配对 + 实际预算控制。
3. 审批续跑接同一事件链 + 取消/重连/终态一致。
4. 长进程交互 + 中途输入队列。

采用固定任务、相同模型设置、同一工作区初态对照，分别测成功率、用户介入、耗时和消耗，避免把模型差异归因于 harness。首批任务：多回合保留约束、测试失败后继续修复、连续两次审批、长进程输出/输入、执行中纠正范围、断线重连、上下文压缩后继续工作。

每项同时检查文件差异、实际工具执行和最终测试结果。通过编译错误修复的单次演示不足以证明长任务可靠性。

本轮验证：`go test ./internal/sessions ./internal/engine` 通过。没有修改运行代码，没有运行完整回归、真实模型对比或原生 UI 验收。
