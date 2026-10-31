# 对照 octo-agent 补齐差距（2026-10-29）

对照 octo-agent-main 列出的 8 项差距的完成情况、用法、验证方式与没做/没验证的部分。

| # | 项目 | 状态 |
|---|------|------|
| 8 | UIA 语义点击 | 完成，实机验证 |
| 4 | 写入护栏（密钥扫描 + 覆盖备份） | 完成 |
| 7 | 小工具（timer 触发器、ANSI 清洗、send_file） | 完成（subagent_inspect 不适用） |
| 6 | 持久“记住的批准” | 完成 |
| 3 | CI / 发布 | 完成（文件已写，未在 GitHub 上跑过） |
| 5 | OS 级沙箱 | 完成（Linux/macOS 只编译验证，未运行） |
| 1 | IM 双向渠道 | 完成（适配器测试通过；未连真实平台） |
| 2 | 手机端 | **暂停**，仅完成服务端远程模式，见下 |

更正：之前说 agentgo 没有“先读后写”跟踪，是错的——`coderuntime` 里已有 `staleCheck`。

## 8 UIA（`internal/computeruse/uia_windows.go`）
`computer_use` 增加 `ui_tree / ui_press / ui_set_value / ui_read / ui_focus`，用原始 COM vtable（`syscall.SyscallN`，无 cgo）。按元素名/AutomationId/控件类型选择，比像素点击稳。
局限：Chromium/Electron 的网页内容即使开了屏幕阅读器标志也基本不暴露（Edge、Cursor 实测）；元素 id 是遍历序号，仅在同一窗口、同一 `max_depth` 内稳定。

## 4 写入护栏（`internal/coderuntime/fs_guard.go`）
写文件命中密钥特征被拒绝；覆盖已有文件前备份到 `<工作区>/.agentgo/overwrite-backups`。`AGENTGO_OVERWRITE_BACKUP=0` 关闭备份。

## 7 小工具
- `watch_event` 新增 `kind=timer`（`delay_seconds` 5~86400），等价 octo 的 schedule_wakeup。
- `execute_bash` 输出去掉 ANSI 转义。
- `send_file`：IM 会话里把文件发回聊天（风险级别 High，需要审批；凭据目录与数据目录被拒绝）。
- `subagent_inspect` 不做：agentgo 的子智能体不可恢复。

## 6 记住的批准
`governance.RememberApproval`：按 会话+工具+参数哈希 记住批准；`RiskCritical` 永远不会被记住的批准放行。IPC `ApproveAndRemember`，前端有按钮。

## 3 CI / 发布
`.github/workflows/ci.yml`（前端 typecheck/test/build → Windows 全量 go build/vet/test；Linux/macOS 构建可移植包；PR 上只对改动文件检查 gofmt）、`release.yml`（打 tag 出 Windows 包 + 校验和，另出 agentgo-cua 的 linux/mac 二进制）、`dependabot.yml`、PR 模板、`SECURITY.md`、`CONTRIBUTING.md`。
没做 goreleaser：`agentgo-cli` 依赖 Wails，只能在 Windows 构建，没有跨平台发布的意义。
`go.mod` 里 eino 指向 `github.com/wk222/eino` 的 replace，CI 需要能访问该仓库（私有的话要配 token）。

## 5 OS 沙箱（`internal/sandbox/os_*.go`）
Linux：Landlock + seccomp，通过“自重新执行垫片”施加（包 `init()` 识别隐藏参数，任何链接了该包的二进制都能当垫片）；macOS：Seatbelt。Windows：不支持，**fail-closed**（2026-10-30 夜：Windows 现在有只防写的沙箱，读取和网络不受限，默认配置仍拒绝执行，见 `docs/SYSTEM_DESIGN_2026-10-30.md`）。
开关：`AGENTGO_SANDBOX_OS=1`（只作用于 `execute_bash`）；`AGENTGO_SANDBOX_OS_NET=1` 允许网络；`AGENTGO_SANDBOX_OS_READ=<目录>;<目录>` 追加只读目录（如 `~/go`）。默认：可写工作区与临时目录，不可读 `$HOME` 下的凭据，无网络。
验证：`GOOS=linux/darwin go vet` 通过、Linux 测试二进制可编译；**真实的 Landlock/Seatbelt 行为没在本机运行**（无 Linux 环境），需要 CI 的 ubuntu 任务第一次跑来确认（内核不支持 Landlock 时测试会自动跳过）。

## 1 IM 双向渠道
- `internal/imchannel`：移植自 octo-agent（MIT）的适配器层——Telegram、飞书、钉钉、企业微信、微信(iLink)、Discord，连同它们的测试（全部通过）。
- `internal/im`：agentgo 自己写的管理层。平台消息 → 会话 `im:<实例>:<会话ID>` → agent → 回复发回平台。
  - **默认拒绝所有人**：实例配置里必须写 `allow_from`（用户 ID 或会话 ID）或 `allow_all: true`；被拒绝的人会收到自己的 ID（限频），方便加入白名单。
  - 命令：`/new`（新会话）、`/stop`（中止）、`/approve` `/deny`（审批，支持按钮的平台显示按钮）、`/help`。
  - 同一会话同时只跑一个任务；掉线自动指数退避重连。
- 运行时在 `bridge` 里新增 `im` 插件：数据目录下的 `channels.yml` 里有启用的实例才会连接。桌面端和 `agentgo-web` 共用数据目录时，只能让一个进程连同一个机器人，另一个设 `AGENTGO_DISABLE_IM=1`。

`<数据目录>/channels.yml` 示例：

```yaml
channels:
  telegram:
    enabled: true
    bot_token: "123456:ABC..."
    allow_from: ["你的 Telegram 用户ID"]
```

后续已补：设置页有渠道配置卡片（不用手写 `channels.yml`），微信(iLink)有扫码登录流程（后端 + 设置页，测试通过）。仍然没有连真实平台验证：适配器与扫码流程靠本地假服务器的单元测试，对真实 iLink 是否一致未知。

## 2 手机端（暂停）
已做：`agentgo-web --remote`（`internal/webshell/remote.go`，含 CORS、`X-Access-Key`/`?access_key=` 鉴权、`/api/ipc/auth-check`、终端二维码，有测试）；`mobile/` 的 Capacitor 壳代码（`typecheck`、11 个 vitest、`bundle-web` 在本机通过）。
没做：`cap add` / Gradle / Xcode 构建、专门的手机 UI（前端是桌面工作台）、附件图片、CI。
`--remote` 本身对没有手机端也有用（别的设备访问），默认关闭。

## 第二轮：对照 octo / WeKnora 剩余差距（除手机端）
- **N2 同参失败止损**（`internal/agent/tool_failure_guard.go`）：同一工具同一参数连续失败后不再重试，直接提示换办法；结果未知（超时、断连）时明确告诉模型"不知道是否已生效"，不让它盲目重复写操作。
- **N3 技能执行准备**（`internal/skills`）：运行技能脚本前检查解释器与依赖是否存在，缺什么直接说；凭据按引用注入，不进模型可见的参数。
- **N4 任务产物交付与版本**：后端 `internal/artifact` + `deliver_file` 工具 + 转录里的 `artifact` 行（不进模型历史）；前端 `ArtifactCard` 显示文件、版本与下载（`/attachments/<name>`）；Markdown 导出带产物行。
- **N6 请求级调试串联**：`AGENTGO_CALL_TRACE`（设置页也可运行时开关）。每次工具调用留 run_id/call_id、原始参数、修复后参数、策略判定、结果、压缩与用量；密钥统一经 `internal/redact` 脱敏；设置页有调试卡片。
- **本地目录刷新**（`index_documents` 的 `sync` 动作，`internal/docindex/sync.go`）：按游标分步遍历大目录；**只有一次完整、无错的遍历且确认文件确实不存在才删索引**，目录整体消失或大批缺失时保留索引并报告。只处理工作区内目录，没有自动定时。
- **文档结构贯穿学习召回链**：学习证据保存页/表/行定位与邻块；新增 `read_learned_neighbors` 读取同一版本的前后文。
- **办公基线评测**（`internal/tools/office_baseline_test.go`，18 个任务：中文制度、xlsx、pdf、跨块、无答案、修改/删除/新增后重问、目录暂时消失）。评分：证据支持、引用能按版本读回原文并带页/表/行、无答案、旧文本泄漏，另记搜索次数、读回次数、耗时（本层不调模型，模型调用为 0）。基线报告 `docs/office-baseline-report.json`：18/18，Hit@6 1.00，MRR 1.00，泄漏 0。
  - 这套评测第一次跑就抓到一个真缺口：问"区块链矿机采购流程"时，只因共享"采购"一词就把采购制度当答案返回。已修：`search_documents` 在最佳片段只覆盖问题不足 34% 的词（问题至少 4 个词）时返回 `weak_match` 并在 message 里提示"文件可能没有答案"。命中仍照常返回，不会降低召回。
  - 局限：这是检索与证据层的回归门，不是模型回答质量评分；语料小且全文检索（无向量），数字不能外推到大库。
- **模型实测**（`internal/tools/office_baseline_model_test.go`，默认跳过；用 `OFFICE_EVAL_BASE/KEY/MODEL` 环境变量启用，密钥不进仓库）：同样 18 个任务，让真实模型用 `search_documents` 工具自己搜、自己答，按事实、出处、诚实（无答案/文件已变更时说明）、不引用旧文本评分。`qwen/qwen3.7-max`：18/18 正确，18/18 带出处，共 36 次模型调用（每题 1 次搜索 + 1 次作答），每题约 4–7 秒；逐题回答在 `docs/office-baseline-qwen3.7-max.json`，已人工通读，答案确实正确。模型在无答案与"文件已变更"两类题里都用到了 `weak_match` 与 stale 提示。
  - 评分是子串匹配，只有 1 个模型、每题 1 次、温度 0，不能说明稳定性或别的模型表现。
  - 网关对"system 消息 + tools"返回 502（该网关的 qwen 路由），测试把指令放进了用户消息。

## 验证记录
- 第二轮：`go build ./...`、`go vet ./...` 干净；前端 `tsc --noEmit`、`npm test`（106 项）、`npm run build` 通过。
- 第二轮全量 `go test -count=1 ./...`：只有 `codetools` 的 gopls 测试在全量并行时超时一次（52s），单独跑 2.5s 通过——负载抖动，不是代码问题（bridge 的 `TestCodetoolsDiagnoseAndFixThroughRegistry` 之前也出现过同样现象）。
- `go build ./...`、`go vet`（受影响包）通过。
- 受影响包的测试通过：computeruse、tools、coderuntime、codetools、trigger、governance、im、imchannel（含各适配器）、webshell、config、sandbox。
- 前端 typecheck + 92 个测试通过。
- 全量 `go test -count=1 ./...`（Windows）：通过。过程中修掉了架构守卫的 4 项（AGENTGO_* 只能在 `internal/config` 声明、新包在 `arch.PackageLayer` 登记、子进程必须隐藏控制台窗口）以及 bridge 的两条目录守卫（插件数量加入 `im`，`computer_use` 作为可选工具不算"死条目"）。
- `go build -tags web ./cmd/agentgo-web/` 通过；`GOOS=linux CGO_ENABLED=0 go vet` 对 sandbox/im/computeruse 通过。
- Linux/macOS 沙箱只做到了"能编译、vet 通过"，没有在真机上跑过。
