# P2.0 内嵌终端身体(单进程、无端口)

状态:✅ 已完成并经真 TUI 验证(2026-10-03)。构建:`go build -o agentgo-tui.exe ./cmd/agentgo-tui`(无需 build tag)。

## 1. 设计

目标:`agentgo-tui` 一个进程里同时跑 AgentGo 大脑和原版 Crush TUI,不监听 TCP、不起子进程。

```
agentgo-tui (一个进程)
 ├─ 大脑: bridge.AppService.CrushHandler() → http.Handler (crushproto /v1)
 └─ 身体: crush/pkg/embedtui.Run(ctx, handler, opts)
          ├─ memListener(net.Pipe) ← http.Server{Handler: handler}
          ├─ client.NewInProcessClient(cwd, memListener.dial)   // Crush 自己的 HTTP 客户端
          ├─ POST /workspaces → workspace.NewClientWorkspace → InitCoderAgent
          └─ ui.New + tea.NewProgram + ws.Subscribe(program)     // 与 root.go 同序
```

要点:
- Go 的 `internal/` 规则禁止 agentgo 直接引用 crush 的 `internal/ui`、`internal/client`,所以在 crush 模块里新增**公开**包 `pkg/embedtui`,把 `root.go` 的 TUI 启动序列提炼进去。
- TUI 走的仍是 client/server 模式的 `ClientWorkspace` + SSE,**行为与 P1 已验证的路径一致**,只是把 TCP/Unix socket 换成了内存管道。
- `slog.SetDefault(DiscardHandler)` 之后由 `crushlog.Setup` 写文件日志,避免任何输出污染终端(`log.Printf` 也经 slog 转发)。`CrushHandler` 另外显式传 no-op `Logf`,不依赖这个副作用。

## 2. 改动清单

| 位置 | 改动 |
|---|---|
| `crush/internal/client/client.go` | 增量:`NewInProcessClient(path, dial)`、`network="mem"`(`dialer`/`buildReq` 各加一个分支);原有 tcp/unix/npipe 行为未变 |
| `crush/pkg/embedtui/embedtui.go` | 新:`Options`、`Run`、`memListener` |
| `crush/pkg/embedtui/embedtui_test.go` | 新:往返、32 并发、长连接流式 + 旁路请求、关闭后拨号失败 |
| `agentgo/internal/bridge/crushproto_plugin.go` | 抽出 `newCrushprotoServer`(TCP 插件与嵌入共用);新增 `(*AppService).CrushHandler()` |
| `agentgo/internal/bridge/crushproto_handler_test.go` | 新:不占端口建 workspace、嵌入 config 含配置模型、nil 保护 |
| `agentgo/cmd/agentgo-tui/main.go` | 新:单二进制入口 |
| `agentgo/go.mod` / `go.sum` | `go mod tidy` 引入 TUI 依赖(只增不减,原有 require 行无变化) |
| `agentgo/scripts/crushproto/drive_tui.py` | 新增 `--embedded` / `--arg`:直接启动 exe,不带 `-H` 和 `CRUSH_CLIENT_SERVER` |

## 3. 已验证

- `go test -race ./pkg/embedtui ./internal/client`(crush):通过。
- `go test -race -run CrushHandler ./internal/bridge`、`go test ./internal/crushproto ./internal/agent ./internal/bridge`(agentgo):通过,说明 TCP 插件重构未回退。
- `go build ./...`、`go vet ./cmd/...` 干净。
- **真 TUI 实测**(`drive_tui.py --embedded` + `fake_llm.py`,临时 `XDG_DATA_HOME`/工作区,已清 `AZURE_OPENAI_*`):
  - 发 `say hello` → 流式回复 `Hello! The secret word is PINEAPPLE.`,顶栏 `fake-r1 via AgentGo (openai)`,上方有 "Thought for 2s" reasoning 块(P1.9 路径在内嵌模式下同样工作)。
  - `ctrl+l` 打开 Switch Model,`fake-r1` 排在首位。
  - **无监听端口**:进程存活期间 `Get-NetTCPConnection` 没有任何 Listen,仅两条出站连接;**无子进程**。
  - `ctrl+c` → 确认框 → `y` 后进程退出,无残留。

## 4. 未覆盖

- F5 / `space` 折叠展开思考块的手动验证(P1 遗留)。"Allow for Session" 尚未单测。
- 恢复后的 `content` 会把工具原始输出和最终回答拼在一起(如 `{"path":...}Created hello.txt`),TUI 里工具结果区下方多出一行 JSON,属观感问题,不影响功能。
- 退出横幅(`exitbanner`)输出在 pty 驱动里没抓到(驱动强制结束前被终止),代码与 `root.go` 一致。

### 4.1 真实 gpt-6-luna 的 reasoning 实测(2026-10-03)

内嵌 TUI 里用真 `gpt-6-luna`(`AGENTGO_REASONING_EFFORT=high`)提问,4 秒答完,**没有思考块**。直接打 Azure 接口对比:

| 接口 | 结果 |
|---|---|
| `POST /openai/v1/chat/completions`(含流式) | `usage.reasoning_tokens=57`(模型确实推理了),但 message 只有 `content/refusal/annotations/role`,流式 delta 只有 `content/refusal/role`:**不返回任何推理文本** |
| `POST /openai/v1/responses` + `reasoning:{effort:high, summary:auto}` | 返回 `type=reasoning` 的 output item,带 `summary_text`(推理摘要) |

结论:链路没有丢东西,是 AgentGo 走 chat/completions 时 gpt-6-luna 不外露推理。P1.9 的 `reasoning_content` / `<think>` 路径对 DeepSeek-R1、Qwen3 等会输出推理文本的模型仍然适用(fake_llm 已验证)。

### 4.2 Responses 通道(已实现并实测)

新增 `internal/agent/responses_model.go`:`responsesModel` 实现 `model.ToolCallingChatModel`,走 `POST {base}/responses`(SSE)。**opt-in**:`AGENTGO_LLM_API=responses`,或 `agentgo-tui -api responses`。

| 项 | 做法 |
|---|---|
| 推理 | `response.reasoning_summary_text.delta` → `Message.ReasoningContent`;不同 `summary_index` 之间补 `\n\n`。下游 `drainADKEvents → ReasoningObserver → Crush thinking` 零改动 |
| 工具 | `output_item.added(function_call)` + `function_call_arguments.delta` → `ToolCall`(`Index` 供 eino 合并);参数只在 `output_item.done` 里时也兜底 |
| 回放历史 | `function_call` 条目**不带 `id`**(`store:false` 下带 id 会要求同时回放配对的 reasoning 条目);`tool` → `function_call_output` |
| 用量 | 自己触发 eino 回调(`IsCallbacksEnabled=true`),`TokenUsage` 才能到 `UsageObserver`(Crush 侧栏 token 数) |
| 强度 | 读 `AGENTGO_REASONING_EFFORT`;`agentgo-tui` 在 Responses 下默认 `medium`(chat 下仍是 `none`);摘要粒度 `AGENTGO_REASONING_SUMMARY`(默认 `auto`) |

验证:
- 单测(`-race`):推理+文本+用量、流式顺序、工具调用(增量/仅 done 事件)、请求体映射、HTTP 错误、流中断、`response.failed`、用量经回调到 observer(流式与非流式)。
- 联网测试 `AGENTGO_LIVE_RESPONSES=1 go test -run TestResponsesLive`:真 gpt-6-luna,105 个流式块,推理摘要完整。
- 真 TUI + 真 gpt-6-luna:思考块 + `Code Write` + `Code View` + 最终回答;文件内容正确;侧栏 `1% (5.4K)`;进程 0 个监听套接字。

**注意**:`medium` 强度下简单问题模型常常不产出摘要(第一次实测就是这样,看起来像"没生效");要稳定看到思考请设 `AGENTGO_REASONING_EFFORT=high`。

**教训**:自写的 eino 模型必须自己触发回调。框架代包装时只给裸 `*schema.Message`,`ConvCallbackOutput` 读不到 `TokenUsage`,症状是侧栏用量悄悄变成 `0% (0)`——功能全对只有这一处异常,极易漏看。另外 `StreamWriter.Send` 返回的是"流已关闭",我最初多取了一次反,导致只发出第一个块;单测第一次运行就抓到了。

### 4.3 权限弹窗(不带 `-yolo`)端到端 —— 挖出两个潜伏 bug(2026-10-03)

场景:真 gpt-6-luna + Responses,让它 `tool_search select:create_workspace_file` 再 `create_workspace_file hello.txt`。`create_workspace_file` 是 RiskMedium,需审批。

| # | 症状 | 根因 | 修复 |
|---|---|---|---|
| 1 | 不出现权限弹窗,模型直接用纯文本"道歉"式回答 | 治理中间件 `StatefulInterrupt` 的 checkpoint 用 gob 编码,`ToolApprovalPause.Arguments` 等里的 `map[string]any`/`[]any` 未注册 → "type not registered";`sendMessageCore` 对 agent 报错**静默回退到无工具的 `ChatOnce`**,错误被吞,内嵌模式日志也被丢弃 | `governance/tool_approval_state.go` `init()`:`schema.RegisterName[ToolApprovalPause]` + `gob.Register(map[string]any{})` + `gob.Register([]any{})` |
| 2 | 点 Allow 后显示 "Approved and executed.",但没执行、无后续回答;`ResumeInterrupt` 10 ms 就返回空内容 | `ContinueAfterApproval`/`ResolveApproval` 把 `*ResumePayload`(指针)交给 `ResumeWithParams`,而中间件里 `tool.GetResumeContext[ResumePayload]` 是**精确类型断言**(值类型),取不到数据 → 又走了一遍 `StatefulInterrupt`,返回的 `PendingApproval` 被忽略 | `governance/tool_approval_state.go` 新增 `resumePayloadFrom(ctx)`,值和指针都接受;`invoke_policy.go` 改用它 |

修复后实测:
- Allow:`hello.txt` 内容 `PINEAPPLE`,出现 `Create Workspace File` 工具块和最终回答。
- Deny(`d`):文件未创建,`granted=false success=true`,无报错。
- 全程 `go vet` 干净,`agent/crushproto/bridge/governance` 测试全绿。

这两个 bug 都不是内嵌终端引入的:**"治理中断 → ADK checkpoint → 恢复"这整条链此前没有任何端到端测试**(governance 测试只测 queue 与单次中间件调用),桌面端同样受影响。

调试开关(均仅在 `AGENTGO_DEBUG_LLM=1` 时输出;`AGENTGO_DEBUG_LLM_FILE=<路径>` 改写入文件,内嵌 TUI 里必须用它,因为标准日志被丢弃):`responses request/stream end`、`engine event:`、`agent run failed ... falling back`、`resume:`、`resolve approval`。

## 5. 历史:曾经的阻塞(已解决,留作经验)

- 当时 agentgo 的 `go.mod` 没有 TUI 依赖,且本机 DNS 不通,`go mod tidy` 无法完成。网络恢复后一次 `go mod tidy` 即成功。
- 原因:agentgo 的 `go.mod` 目前没有 TUI 依赖(bubbletea/v2、lipgloss/v2、bubbles/v2、ultraviolet、x/term、colorprofile…)。本机当时 DNS 不通(goproxy.cn / proxy.golang.org 均无法解析,本地代理端口 7897/7890/10809/10808 都连不上),`go mod tidy` 无法完成。
- 试过且**失败**的离线办法(别重复):
  1. `GOPROXY=off go mod tidy` / `-mod=mod`:需要缓存里没有的旧模块 `go.mod`(`imaging@v1.6.2 → x/image@2019`、`x/vcr` 等)。
  2. 用脚本把 crush 的 require 取高版本合并进 agentgo `go.mod`,再逐个剔除缓存缺失的测试依赖:剔掉 8 个后仍卡在 `imaging → x/image@2019`(老式非剪枝模块触发完整模块图加载)。
  3. `GOWORK` 工作区(agentgo + crush):`./cmd/...` 报"目录不属于工作区模块",按导入路径构建则仍去解析 crush 伪版本。
- `go.mod`/`go.sum` 已逐字还原为改动前,仓库无残留。

## 6. 下一步

1. F5 / `space` 折叠展开思考块手动验证(P1 遗留);"Allow for Session" 单测。
2. 给"治理中断 → checkpoint → 恢复"补一个自动化端到端测试(假模型发工具调用 → 中断 → `ResumeInterrupt` 值/指针两种 payload → 工具执行),防止这两个 bug 回归。
3. `sendMessageCore` 的静默回退至少要把原始错误展示给用户(或在 `ChatOnce` 回答前缀提示),别再吞掉。
4. 恢复结果里去掉工具原始输出拼接。
5. 考虑 P2.1:让 `agentgo`(桌面版)主程序提供 `tui` 子命令,共享同一个大脑实例,而不是单独的 `agentgo-tui`。

## 7. 经验

- **环境变量要看"用户级持久"**:`AZURE_OPENAI_*` 是用户级环境变量,`Remove-Item Env:` 只影响当前进程且不一定跨命令保留。第一次内嵌实测因此悄悄走了真实 Azure(顶栏显示 `gpt-6-luna via AgentGo (azure)` 才发现,只写进了临时目录的 config.json)。**规程**:在启动被测进程的同一条命令/Job 内清除,并用 fake 模型的暗号(PINEAPPLE)当断言,看到真模型名就立刻判失败。
- 端口/进程检查必须在被测进程**存活期间**采样,别在驱动结束之后(第一次 `pids:` 为空就是这样,结论无效)。

- **"成功但什么都没发生"先看耗时**:恢复 10 ms 就返回,不可能执行了工具+模型调用,这一个数字直接把嫌疑锁到"空转/再次中断"。
- **eino `GetResumeContext[T]` / `GetInterruptState[T]` 都是精确类型断言**,指针≠值;`any` 里的具体类型过 checkpoint(gob)还必须 `gob.Register`。凡是"静默回退"的代码路径,调试时第一步是让它把原始错误打印出来。
- 跨模块想复用 `internal/` 的 UI,正解是在被依赖模块里开 `pkg/` 公开包,而不是复制代码。
- 把"依赖解析"当成独立风险早点验证:写完代码才发现缺 `go.sum` 依赖、又没网,浪费了大量步骤。下次动手前先 `go list -deps` 或确认网络。
- PowerShell:`go mod edit -droprequire=a.b/c` 必须加引号,否则参数在 `.` 处被拆开;没有 heredoc。
- 全局环境变量(`GOPROXY/GOFLAGS/GOWORK`)会在 shell 会话里持久,试验后要清理(本次已清)。
