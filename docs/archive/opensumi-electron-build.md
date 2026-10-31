# AgentGo × OpenSumi 官方 Electron 桌面客户端打包指南

本项目已完全配置支持 **OpenSumi 官方重型 Electron 桌面应用打包方案**（类似于 CodeFuse IDE / VS Code 官方架构）。

---

## 一、 整体打包架构

```
┌─────────────────────────────────────────────────────────────────┐
│              AgentGo OpenSumi IDE.exe (Electron 安装包)          │
├─────────────────────────────────────────────────────────────────┤
│  Electron 主进程 (dist-electron/main.js)                        │
│  ├── 原生窗口生命周期管理 (BrowserWindow / Native Menu / IPC)    │
│  ├── 自动派生与托管 OpenSumi Node 运行服务 (dist-node/server.js) │
│  └── 自动加载 OpenSumi AI-Native 界面 (dist/index.html)         │
├─────────────────────────────────────────────────────────────────┤
│  嵌入式额外资源 (extraResources)                                │
│  └── agentgo.exe (Go 原生编译引擎，提供 Eino / ACP 协议支持)     │
└─────────────────────────────────────────────────────────────────┘
```

---

## 二、 核心配置文件

1. **主进程入口**：`frontend/src/main/index.ts`
   - 管理主窗口创建、原生菜单、生命周期，并在后台拉起 Node 服务。
2. **主进程 Webpack 编译**：`frontend/configs/webpack.main.config.js`
   - 输出至 `frontend/dist-electron/main.js`。
3. **打包配置**：`frontend/package.json` 中的 `build` 节点
   - 目标：NSIS 安装器与 Portable 便携版 (`.exe`)。
   - 自动将 `../agentgo.exe` 复制到 Electron 的 `resources/agentgo.exe`，免除用户额外配置环境变量。

---

## 三、 一键构建与打包命令

### 方式一：使用内置自动化脚本（推荐）
```powershell
.\scripts\build-electron.ps1
```

### 方式二：手动执行分步命令
```bash
# 1. 编译 Go 原生 ACP 引擎
go build -o agentgo.exe ./cmd/agentgo

# 2. 避免 C 盘爆仓，重定向 Electron 缓存至 D 盘 (可选但强烈建议)
$env:ELECTRON_MIRROR="https://npmmirror.com/mirrors/electron/"
$env:ELECTRON_BUILDER_BINARIES_MIRROR="https://npmmirror.com/mirrors/electron-builder-binaries/"
$env:ELECTRON_CACHE="D:\devtools\electron-cache"
$env:ELECTRON_BUILDER_CACHE="D:\devtools\electron-builder-cache"

# 3. 进入前端并编译产物
cd frontend
pnpm install
pnpm build

# 4. 执行 electron-builder 生成 Windows .exe
pnpm pack
```

打包完成后，安装包将输出至根目录的 **`out/`** 文件夹中：
- `out/AgentGo OpenSumi IDE-1.0.0.exe` (NSIS 安装向导)
- `out/AgentGo OpenSumi IDE 1.0.0.exe` (免安装便携版)
- `out/win-unpacked/` (解压可直接运行的绿色版文件夹)
