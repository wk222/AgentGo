# 可复现构建

## 原则

- 提交到 Git 的 **`go.mod` / `go.sum`** 不得包含本机绝对路径 `replace`。
- 依赖版本以 **Go module proxy**（`github.com/cloudwego/eino` 等）为准。
- 本地联调 Eino/Wails 源码时使用 **`go.work`**（已 gitignore），不要改提交的 `go.mod`。

## 标准构建（CI / 新机器）

```powershell
cd agentgo
go version   # 建议 Go 1.24+
go mod download
go build -o bin/agentgo.exe ./cmd/agentgo
```

产物请写入 `bin/`，勿在仓库根目录留下 `agentgo.exe` / `agentgo_build.exe`。

## 完整 Wails v3 打包（推荐发布用）

需要安装 [Wails v3 CLI](https://v3.wails.io/)（命令名一般为 `wails3`）。

**推荐：安装到 D 盘（本机已配置）**

```powershell
cd agentgo
powershell -ExecutionPolicy Bypass -File scripts/install-wails-tools.ps1
# 新开终端后：
wails3 doctor
```

| 路径 | 内容 |
|------|------|
| `D:\dev\go\bin\wails3.exe` | Wails CLI |
| `D:\dev\go\pkg\mod` | Go 模块缓存（GOMODCACHE） |
| `D:\dev\nsis\makensis.exe` | NSIS 打包 |

用户 PATH 已追加 `D:\dev\go\bin` 与 `D:\dev\nsis`（需**新开终端**生效）。

手动安装：

```powershell
$env:GOBIN="D:\dev\go\bin"; $env:GOMODCACHE="D:\dev\go\pkg\mod"
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
winget install NSIS.NSIS --location D:\dev\nsis
wails3 doctor
```

### 生产构建（带图标、无控制台窗口）

```powershell
cd agentgo
wails3 build
# 等价于: wails3 task build
```

与 `go build` 的差异：

- 输出仍在 `bin/agentgo.exe`，但使用 `-H windowsgui`（不弹黑色控制台）
- 嵌入 `build/windows/icon.ico` 与版本信息（`info.json` / manifest）
- 使用 `-trimpath -ldflags="-w -s"`，体积通常比 debug `go build` 更小

### 安装包（NSIS）

`wails doctor` 若提示 **NSIS: Not Installed**，需先安装 [NSIS](https://nsis.sourceforge.io/) 并保证 `makensis` 在 PATH 中，然后：

```powershell
wails3 package
# 或: wails3 task package
```

会在 `bin/` 下生成 **`agentgo-amd64-installer.exe`**（内含应用与 WebView2 引导逻辑；`build/windows/nsis/` 需保留 `project.nsi`）。

### 开发模式

```powershell
wails3 dev -config ./build/config.yml
# 或: wails3 task dev
```

前端当前为静态资源 `frontend/dist/`（无 npm 流水线）；改 UI 后直接改 `dist` 再 `wails3 build`。

### 配置入口

| 文件 | 作用 |
|------|------|
| `Taskfile.yml` | `APP_NAME`、`MAIN_PKG=./cmd/agentgo` |
| `build/config.yml` | 产品名、版本、dev 监视规则 |
| `build/windows/info.json` | Windows 文件版本/公司名等 |
| `build/appicon.png` | 图标源图；改后运行 `wails3 task common:generate:icons` |

## 本地 Eino / Wails 源码联调

1. 将 `eino-main`、`eino-ext-main` 放在与 `agentgo` 同级目录（或自定义路径）。
2. 复制 `go.work.example` 为 `go.work`，按本机路径修改 `use` 段。
3. 执行 `go work sync` 后构建。

```powershell
Copy-Item go.work.example go.work
# 编辑 go.work 中的路径
go work sync
go build -o bin/agentgo.exe ./cmd/agentgo
```

`go.work` 与 `go.mod.local` 已在 `.gitignore` 中忽略。

## 清理构建缓存（磁盘不足时）

勿在仓库根目录留下 `agentgo.exe` / `agentgo_build.exe`（每个约 65–70MB，且已在 `.gitignore`）。

```powershell
cd agentgo
powershell -File scripts/clean-gobuild.ps1
```

- 会删除根目录与 `bin/` 下的 stray `.exe`，并执行 `go clean -testcache`、`-cache`、`./...`。
- 若 `go clean -cache` 报 **Access is denied**，先关掉正在跑 `go test` / `go build` 的终端或 IDE 编译，再重试。
- **不要**随意 `go clean -modcache`，会删掉模块缓存并触发全量 re-download。

**Memory → Compose 异步子图**：见 `memory-eino.md`；当前 P2，收益主要在 Milvus/可观测性，聊天延迟瓶颈不在此（JOURNAL/REFLECT 已 goroutine 异步）。
