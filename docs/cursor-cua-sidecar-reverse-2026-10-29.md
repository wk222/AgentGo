# Cursor Computer-Use Sidecar 逆向分析（2026-10-29）

目标：`C:\Users\user\.cursor\computer-use-sidecar\cursor-cua-sidecar.exe`
方法：IDA MCP（idalib + Hex-Rays），字符串/serde 字段表/交叉引用定点分析。

## 1. 身份与构建

| 项 | 值 |
|---|---|
| 产品 | Cursor Computer-Use sidecar **v0.0.33**（build 33，protocolMajor 1） |
| 适配 | Cursor `3.24.0-pre.15`（lane=explicit, sourceBranch=main, sha ccd3a74d） |
| 语言/工具链 | **Rust 1.98.1**，`x86_64-pc-windows-msvc` |
| 大小/哈希 | 1,279,272 B；SHA256 `a84c9fde…56c07`（Anysphere, Inc. 签名，thumbprint `42de18ee…`） |
| 分发 | `https://downloads.cursor.com/computer-use-sidecar/artifacts/sha256/<sha>/Cursor-Computer-Use-0.0.33-win32-x64.exe` |
| 代码规模 | 2623 个函数（仅 67 个有符号名），10411 条调用边，5 段 |

内部源码路径（panic 信息泄露）：`packages\computer-use\windows\src\`
- `sys\service.rs` — 主服务/连接循环
- `sys\pipe.rs` — 命名管道
- `sys\presence_stop.rs` — Stop 覆盖层
- `zoom.rs`、`drag_path.rs`

依赖：miniz_oxide 0.8.9（PNG/deflate 编码截图）、rustc-demangle、uuid。

## 2. 架构总览

```
Cursor 主进程（Electron, 签名）
   │  ① 启动前写 launch token 文件
   │  ② spawn cursor-cua-sidecar.exe
   ▼
\\.\pipe\<管道名>  （owner-SID DACL，reject-remote，first-instance 互斥体）
   │  ③ 握手帧（一行 JSON）
   ▼
sidecar：截屏(GDI/D3D11+DComp→PNG) + 注入(SendInput) + Stop 覆盖层 + 会话/权限状态机
```

### 启动绑定（launch binding）
- 路径：`%LocalAppData%\.cursor\cursor-computer-use\cua-launch-<用户SID>.token`（`SHGetKnownFolderPath(FOLDERID_LocalAppData)` + `ConvertSidToStringSidW` 拼接）
- 启动进程必须**先写 token 再启动 sidecar**；每次连接的握手帧必须携带该 token（"the launching process must write the launch token before starting the sidecar (see README.md, \"Client handshake contract\")"）
- 校验：非空、必须为 hex（固定长度，"launch token must be N hex characters"）、文件 owner 必须是当前用户、DACL ACE 类型检查
- 启动横幅：`transport ACLs ACTIVE (owner-SID DACL, reject-remote, first-instance); launch binding ACTIVE (owner-only token under %LocalAppData%, required on every connection); peer signer pinning ...`

### 对端验证（peer signer pinning）
- `GetNamedPipeClientProcessId` → `OpenProcess`/`QueryFullProcessImageNameW` → `WinVerifyTrust` + `WTHelperGetProvSignerFromChain` + `CertGetNameStringW`（wintrust/crypt32）——校验连接方是**签名过的 Cursor**（签名者钉扎）
- `OpenProcessToken` + `GetTokenInformation(TokenIntegrityLevel)` 完整性级别检查

## 3. 线协议（换行分隔 JSON）

**HandshakeRequest（3 字段）**：`{"cua": "...", "version": <int>, "token": "<hex>"}`（serde 字段表 @0x140100d18）
**欢迎帧**：`{"cua":"welcome","version":<int>,...}`（@0x1401004ac）
握手看门狗 + 超时；"client sent no handshake within {}s; dropping"；空闲 N 秒无客户端自动退出。

**RpcRequest（10 字段，@0x140100d6a 起）**：
`id`、`method`、`requestID`、`Name`、`arguments`、`mode`、`deadlineEpochSeconds`、`timeoutSeconds`、`startedAtEpochSeconds`、`sessionId`

**method 取值**（serde 匹配表 @0x1400fc040 区域 + 各处）：`screenshot`、`zoom`、`click`、`move`、`scroll`、`drag`、`typing`、`press_key`、`batch`、`remote`、`stop`、`wait`；`mode ∈ {remote, companion}`（Windows 仅 remote："companion mode is not supported on Windows (remote mode only)"）。

**响应信封**：`result` / `errorCode` / `escalation{recommended, reason}` / `permissionStatus` / `structuredContent` / `screenshotBefore` / `zoomId` / `sessionId`。
错误码与 MCP 层升级码一一对应：`capture_failed`、`invalid_arguments`、`timeout`、`screenshot_required`、`session_busy`、`permission_required`、`secure_desktop`、`input_desktop_unavailable`、`target_elevated`、`sidecar_unavailable`、`outcome_unknown`、`session_required`、`user_aborted`、`handshake_rejected`；escalation.recommended ∈ `retry` / `ask_user` / `use_different_tool` / `stop`。

## 4. 截屏管线

- 采集：GDI（`BitBlt`/`CreateDIBSection`/`GetDIBits`）+ **D3D11**（`D3D11CreateDevice`）+ **DComp**（`DCompositionCreateDevice`，用于 Stop 覆盖层合成）
- 编码：miniz_oxide deflate → PNG（"png encode: " 错误路径）
- 固定 1456×840 canvas，不足处 letterbox 填充；zoom 有 region 校验（角点、太薄、落在黑边上均报错）、`zoom-<n>` 编号、zoom 只在本会话有效
- DPI：`SetProcessDpiAwarenessContext` 强制高 DPI 感知；若被兼容性垫片强制成不感知（200% 屏坐标全错）则**拒绝服务并报错**，要求清除 AppCompat 设置；Windows 版本闸门：build 19041（2004）以下有功能降级路径（WDA_EXCLUDEFROMCAPTURE）

## 5. 输入注入

- `SendInput` + `MapVirtualKeyExW`/`VkKeyScanExW`/`GetKeyboardLayout`
- typing 过滤不可见字符/加速键风险（"use computer_use_press_key if a key press was intended"）
- batch 中破坏性按键（删除/剪切/关闭/锁定）必须 `allow_destructive: true`，否则整批拒绝
- drag：`path` 数组 ≥2 个 {x,y}，>2 点时被限制在首点所在窗口内，超窗拒绝
- deadline（`deadlineEpochSeconds`）超时后批停，停止原因枚举：`refused` / `no_change` / `foreground_changed` / `deadline`；部分落盘的输入会提示"先截图确认，不要盲重试"

## 6. Stop 覆盖层（presence_stop.rs）

- 窗口类 `CursorComputerUseStop`，分层窗口（`SetLayeredWindowAttributes`）+ DComp 合成
- `SetWindowDisplayAffinity(WDA_EXCLUDEFROMCAPTURE)`：**模型截图里看不到 Stop 按钮**；旧系统回退 `WDA_MONITOR` 会出现模型可见的黑矩形（代码里有明确处理注释）
- 反代理点击：命中测试 + `GetMessageExtraInfo`（注入标记）——"an agent press reached Stop; ignored"，即只有真人点的 Stop 才生效
- 全局热键 `RegisterHotKey` 可停止会话；Stop 后 "no input is sent until a new remote-control session starts"

## 7. 会话与安全状态机

- 单会话独占桌面：他人持有时返回 `session_busy`，"the user decides which one continues"
- `OpenInputDesktop`/`GetUserObjectInformationW` 检测 UAC 安全桌面 → `secure_desktop`（只有用户能操作）
- 提权目标检测（`target_elevated`）、权限缺失 → `permission_required`（Windows 无按应用授权，"Windows requires no per-app input or screen-capture permission"）
- Stop = 用户中止 → `user_aborted`，"Do not call input or control tools again this turn"

## 8. 评价

安全设计相当扎实：管道 DACL + launch token + 对端签名钉扎三重防线，防的是"任意本地进程冒充 Cursor 客户端连上来注入输入"；Stop 覆盖层对模型不可见且忽略注入点击，闭环了"AI 自己关掉自己刹车"的路径。协议是明文 JSON（本地 IPC，无加密必要）。潜在弱点：`GetSystemMetrics`/坐标换算依赖 DPI 一致性，二进制里对 DPI 垫片有专门拒绝逻辑，说明踩过坑。

## 9. 复现备忘

- IDA 会话 id `d3f4b138`（idle TTL 自动退出），IDB 落在 exe 旁 `cursor-cua-sidecar.exe.i64`
- 关键地址：
  - 主服务函数 `sub_140071A10`（0xE74A 字节：管道创建/握手/会话循环）
  - 输入命令分发 `sub_140025C50`、batch `sub_14000D660`、screenshot `sub_140071A10` 内部
  - serde 字段表 @0x140100c80–0x140100d80；方法匹配表 @0x1400fc040
