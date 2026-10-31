# Win7 兼容版（分支 `win7`，浏览器模式）

日期：2026-10-26

## 做法
Win7 没有可用的 WebView2，所以这个版本不开原生窗口：`AgentGo-web.exe` 在 `127.0.0.1:8788` 起 HTTP 服务，
自动用 Chrome / Edge（应用窗口）、Firefox 或系统默认浏览器打开界面。

- 入口：`cmd/agentgo-web`（`-tags web`）。该标签下 Wails / WebView2 完全不进二进制（打包脚本会检查）。
- 前端不改：它通过 `/wails/runtime.js` 的 `Call.ByName` / `Events.On` 访问后端。
  `internal/webshell/runtime.js` 是这个接口的浏览器实现：
  调用 → `POST /api/ipc/call`（按方法名反射调用 `AppService`），事件 → SSE `/api/ipc/events`。
- 后端事件出口统一为 `internal/bridge/events.go`（`emitEvent` / `AddEventSink`）；桌面版仍转发给 Wails 窗口，行为不变。
- 桌面专属部分按构建标签拆开：`host_desktop.go` / `host_web.go`、`workspace_dialog_{desktop,web}.go`。
  浏览器版的“选择文件夹”走 PowerShell 的 FolderBrowserDialog（`internal/hostui`），“独立窗口”改成新标签页。
- 安全：只监听回环地址；校验 Host / Origin；`/api/ipc/*` 需要 HttpOnly Cookie（令牌在数据目录 `web-token`，首次用 `?t=` 带入）。
- IE 访问会得到“请安装 Chrome 109 / Firefox 115 ESR”的提示页。

## 工具链
`thongtech/go-legacy-win7` v1.27.2-1，解压到任意目录，用环境变量 `AGENTGO_WIN7_GO` 指向它的 `bin\go.exe`（`scripts/release-win7.ps1`、`scripts/regress.ps1` 都读它；构建缓存目录用 `AGENTGO_WIN7_GOCACHE` 指定）。
打包：`scripts/release-win7.ps1`（用 legacy Go、`-tags web` 跑全部 Go 测试、前端检查与构建、编译、校验）。

## 办公工具随包内置（内网电脑也能用）
`create_docx` / `create_xlsx` / `read_xlsx` / `edit_xlsx` / `create_pptx` / `inspect_pptx` 和四个办公技能
（`office`、`office-xlsx`、`office-pptx`、`office-docx`）都编进 exe，不需要 Go、Python、Node、Office 或联网。
细节、限制和验证记录见 `docs/office-tools-plan.md`。

## 已验证
- 默认构建和 `-tags web` 构建下 `go test ./...` 通过（用 legacy Go）。
- 本机浏览器端到端：令牌登录、聊天流式回复、会话落库、标题自动生成。
- PE 头标记 Windows 6.1；静态导入表只有 kernel32 中 Win7 就有的函数。

## 未验证（需要真实 Win7）
- 在 Win7 上能否启动运行；Chrome 109 / Firefox 115 ESR 下界面是否正常（前端构建目标是 Vite 默认的 Chrome 107 / Firefox 104）。
- 终端面板用到 ConPTY（Win10 1809+），Win7 上预计不可用。
- 自动选择浏览器、文件夹对话框在 Win7 上的表现。

## 与主线同步
- 浏览器模式的改动集中在新增文件，但 `internal/bridge` 里有一批把 `s.app.Event.Emit` 换成 `s.emit` 的小改动；
  这些对桌面版是行为不变的重构，可以直接合回 `master`。
- 主线更新后合并到 `win7`，再用 legacy Go 跑 `scripts/release-win7.ps1`；主线若用了新版 Go 才有的 API 需要在这里修。
