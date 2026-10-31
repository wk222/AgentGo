# agentgo Computer Use（2026-10-29）

让模型"看屏幕 + 操作鼠标键盘"。设计来自对 Cursor computer-use sidecar 的逆向
（`docs/cursor-cua-sidecar-reverse-2026-10-29.md`），Win32 基座移植自
octo-agent `internal/computer`（MIT），进程隔离层用 go-winio。

## 怎么用

```powershell
$env:AGENTGO_COMPUTER_USE = '1'                 # 打开才会注册 computer_use 工具（默认关）
# 可选：让截屏/注入跑在独立进程里（例如以管理员身份启动，才能操作管理员窗口）
go build -o agentgo-cua.exe ./cmd/agentgo-cua   # 想无控制台窗口：-ldflags "-H=windowsgui"
$env:AGENTGO_COMPUTER_USE_SIDECAR = 'D:\path\agentgo-cua.exe'
```

**刹车：`Ctrl+Alt+Esc`**（全局热键，真实按键，模型发不出来）。按下后拒绝一切输入，
每次被拒都会续期 20s，只有安静 20s 后的新一轮才恢复。

## 结构

| 文件 | 职责 |
|---|---|
| `internal/computeruse/errors.go` | 错误码 + escalation（retry/ask_user/use_different_tool/stop），与 Cursor 同一套 |
| `types.go` | `Request`（工具入参=RPC 入参）、`Step`、`Substrate` 接口 |
| `canvas.go` | 截图缩放到 ≤1456×840（不放大）；截图/zoom 像素 ↔ 物理屏幕像素 |
| `controller.go` | 动作调度、`screenshot_required` 门控、Stop、batch、动作后自动截图 |
| `substrate_windows.go` | GDI 截屏 + `SendInput`；预检 secure desktop（UAC）与 UIPI 高完整性目标 |
| `rpc.go` / `token.go` | JSON 行协议、握手、launch token（每次连接重读） |
| `pipe_windows.go` | **go-winio**：SDDL `D:P(A;;GA;;;<我的SID>)(A;;GA;;;SY)`、first-instance、拒绝远程 |
| `hotkey_windows.go` | `RegisterHotKey` 停止热键 |
| `cmd/agentgo-cua` | sidecar 进程 |
| `internal/tools/computer_use.go` | `EnhancedInvokableTool`：结果 = 文本 + PNG 图片 part |

## 已验证（真实输出，不是推断）

- `go test`：computeruse（含假基座 14 项逻辑、RPC 5 项、真实命名管道 4 项）、tools、governance、config、agent(Mode|Policy|Tool|Canvas) 全绿
- 真实屏幕：3840×2160 → 1456×819 PNG 314 KB；zoom 原生 1280×720
- 真实子进程：`agentgo-cua.exe` 被拉起 → 管道握手 → 截屏；错 token 被拒；越界点击被拒
- `INPUT` 结构体布局断言（amd64 = 40 字节，错了 SendInput 会静默失败）
- linux/darwin（CGO 关）交叉编译通过（stub）

## 踩到的坑

- **递归类型炸栈**：`Request.Steps []Request` 让 eino 的 schema 反射无限展开 → stack overflow。
  改成独立的非递归 `Step`。以后工具入参**禁止自引用类型**，且一定要有 schema 生成测试。
- `jsonschema:"description=..."` 描述里不能有逗号。
- 终端里 `$_` 会被吞，别在 shell 里写管道变量。

## 没做 / 待办（按价值排序）

1. **未验证**：真实 LLM 供应商对"工具结果里带图片"的支持。OpenAI chat 的 tool 消息只收文本；
   Anthropic 收图。已兜底：文本里附 `Screenshot file: <path>`，可交给 `vision_advisor`。
   下一步用真实模型跑一次完整任务，必要时把图片改投成下一条 user 消息。
2. **UIA 语义点击**：octo 的 `uia_windows.go`（按 role+label 点控件）没移植。像素点击对中文 UI、
   小控件会飘，UIA 是质量上限所在。
3. **Stop 的可见性**：Cursor 有一个模型截不到的 Stop 药丸（`WDA_EXCLUDEFROMCAPTURE` + DComp）；
   这里只有热键、无可视提示。至少应在 UI 状态栏显示"computer use 运行中，Ctrl+Alt+Esc 停止"。
4. **对端签名钉扎**（`WinVerifyTrust`）没做，目前靠 管道 DACL + launch token。
5. **只支持主显示器**；GDI 截不到部分 DRM/硬件叠加内容，必要时换 Windows Graphics Capture。
6. 开关目前只有环境变量，应接入 Wails 设置页。
7. Win7 分支（`docs/win7-branch-plan.md`）未实测；`SetProcessDpiAwarenessContext` 已做存在性判断。
8. 没有"用户在动鼠标就中止"的检测（Cursor 有 `foreground_changed` 停止原因）。
