# AgentGo AI-Native IDE 重构与原生融合计划 (Wails v3 + OpenSumi Lite + AgentGo Core)

## 一、 核心架构目标

摆脱笨重且脆弱的 Electron + Node.js 运行时，转向 **单二进制、超轻量、高性能的现代 AI IDE 架构**：

```
┌────────────────────────────────────────────────────────┐
│                   AgentGo-IDE.exe                      │
│                                                        │
│  ┌──────────────────────────────────────────────────┐  │
│  │ Windows Edge WebView2 渲染层 (OpenSumi Lite UI)   │  │
│  │ ├─ Monaco Editor (编辑器核心 + 内置语法高亮)       │  │
│  │ ├─ Xterm.js (终端视图，对接 Go ConPTY)           │  │
│  │ ├─ AI Native (Ctrl+K 行内编辑, 侧边助手, 智能补全) │  │
│  │ ├─ Workflow DAG 画布 & 实验监控面板 (InnerApps)  │  │
│  │ └─ TypeScript Bridge Adapter (Wails Typed Bindings)│
│  └────────────────────────┬─────────────────────────┘  │
│                           │ Wails v3 IPC & Events      │
│  ┌────────────────────────▼─────────────────────────┐  │
│  │            AgentGo IDE Runtime (Go 核心)          │  │
│  │                                                  │  │
│  │ ┌──────────────────────────────────────────────┐ │  │
│  │ │ ProcessSupervisor (进程监管地基)              │ │  │
│  │ │ ├─ Windows Job Object (自动级联抹除子进程树)   │ │  │
│  │ │ ├─ Windows ConPTY (aymanbagabas/go-pty)      │ │  │
│  │ │ └─ 嵌入式 Ripgrep (rg.exe --json 极速代码检索)│ │  │
│  │ └──────────────────────────────────────────────┘ │  │
│  │                                                  │  │
│  │ ┌──────────────────────────────────────────────┐ │  │
│  │ │ Workspace & FS Engine (文件系统与观察者)     │ │  │
│  │ │ ├─ 统一 URI 规范 (file:///)                  │ │  │
│  │ │ ├─ 安全沙箱与原子写 (Temp -> Sync -> Replace) │ │  │
│  │ │ └─ 递归 fsnotify (100ms 批处理与防抖)        │ │  │
│  │ └──────────────────────────────────────────────┘ │  │
│  │                                                  │  │
│  │ ┌──────────────────────────────────────────────┐ │  │
│  │ │ AgentGo AI Core (智能体原生核心)             │ │  │
│  │ │ ├─ Eino 编排引擎 (v0.10.0-alpha.32)          │ │  │
│  │ │ ├─ PurrCat 7 大特性 (KeyMgr, Heartbeat, 范式)│ │  │
│  │ │ ├─ JobMonitor 任务静默监控                   │ │  │
│  │ │ └─ 原生 Inline Edit / Ghost Text FIM 引擎    │ │  │
│  │ └──────────────────────────────────────────────┘ │  │
│  └──────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────┘
```

---

## 二、 关键实现分步路线图

### 阶段一：IDE 运行时三大地基（最底层支撑）
- [ ] **1.1 ProcessSupervisor 进程监管器**
  - 使用 Windows API 创建专属 `Job Object`（设置 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`），保证主程序退出或崩溃时，所有子进程（编译器、脚本、命令行）物理级联终止，杜绝孤儿僵尸进程。
  - 封装统一的 `ProcessSpec` 与 `ProcessHandle` 接口，覆盖标准进程与伪终端进程。
- [ ] **1.2 WorkspaceFS 文件系统与安全沙箱**
  - 全局统一 `file:///` URI 解析层与 Path Guard（防止 Agent 逃逸工作区越权读写）。
  - 实现原子写操作（`write temp -> flush -> sync -> rename replace`）与 `expectedVersion` 冲突校验机制。
  - 递归 `fsnotify` 包装器，集成 100ms 抖动合并（Coalesce Batch）与 `ErrEventOverflow` 全局重新扫描。
- [ ] **1.3 Terminal 虚拟终端引擎**
  - 引入 `github.com/aymanbagabas/go-pty`，实现 Windows ConPTY 原生驱动。
  - 终端输出设立 20~30ms 聚合缓冲队列（60FPS 帧率控制），批量向前端发射 Wails Event，防止大日志量刷屏导致 UI 卡死。

### 阶段二：检索与版本控制加速引擎
- [ ] **2.1 内嵌 Ripgrep 检索服务**
  - 将编译好的 `rg.exe` 嵌入二进制，运行时按哈希解压至本地缓存目录。
  - 对接 ProcessSupervisor 运行 `rg --json`，支持流式解析、随打随搜与即时取消（Context Cancel）。
- [ ] **2.2 Git CLI 极速解析层**
  - 基于系统 `git` 命令执行 `git status --porcelain=v2 -z --branch`，精准解析工作区增删改查状态。
  - 对接文件系统观察者，设置 300ms 防抖自动刷新 Git 状态。

### 阶段三：语言诊断与轻量编译器集成（实用主义路线）
- [ ] **3.1 极简编译器诊断执行器**
  - 不引入数万行复杂的通用 LSP Client 协议，而是通过 ProcessSupervisor 执行原生的编译器检查工具（如 `go vet`、`tsc --noEmit`、`ruff/flake8` 等），输出标准的 LSP Marker Diagnostic 结构，秒级为前端 Monaco 和 Agent 纠错提供事实依据。

### 阶段四：Wails v3 前后端通信桥接与 OpenSumi Lite 适配
- [ ] **4.1 Wails IPC 接口与事件总线定义**
  - 扩展 `internal/bridge/AppService`，将 FS、Terminal、Process、Search、Git 及 AgentGo 核心服务完整注册到 Wails 服务列表中。
  - 定义事件主题：`terminal:data`、`workspace:filesChanged`、`agent:thought`、`agent:stream`。
- [ ] **4.2 OpenSumi Lite 前端适配器**
  - 将之前写好的 Workflow 画布、InnerApps（任务监控与记忆图谱）、Ctrl+K 行内编辑、底部状态栏指示器等模块，直接对接 Wails 原生绑定（`window.go.main.AppService`）与事件监听。

### 阶段五：单二进制编译与验证闭环
- [ ] **5.1 资源嵌入与主程序入口切换**
  - 使用 `//go:embed all:dist` 嵌入 OpenSumi Lite 静态构建物。
  - 完善 `cmd/agentgo/main.go`，直接使用 Wails v3 调起 Windows Edge WebView2 加载嵌入式静态资源。
- [ ] **5.2 编译、打包与全链路测试**
  - 执行单条 Go 构建命令，输出约 25MB~35MB 的单文件绿色版 `agentgo.exe`。
  - 验证：文件树读写、ConPTY 终端交互、代码搜索、行内 AI 编辑、后台任务静默监控全流程跑通。

---

## 三、 核心接口契约设计

### 1. ProcessSupervisor
```go
type ProcessKind string
const (
    KindTerminal ProcessKind = "terminal"
    KindTask     ProcessKind = "task"
    KindSearch   ProcessKind = "search"
    KindGit      ProcessKind = "git"
)

type ProcessSpec struct {
    ID      string
    Kind    ProcessKind
    Command string
    Args    []string
    Cwd     string
    Env     []string
    UsePTY  bool
    Cols    int
    Rows    int
}
```

### 2. FileService
```go
type FileStat struct {
    URI       string `json:"uri"`
    Name      string `json:"name"`
    IsDir     bool   `json:"is_dir"`
    Size      int64  `json:"size"`
    ModTime   int64  `json:"mod_time"`
    Version   string `json:"version"`
}
```

---

## 四、 成果交付物
1. 单一可执行文件：`agentgo.exe`（自包含 GUI、AI 引擎、虚拟终端与文件系统，无须安装 Node.js、Python 或外部 Electron 壳）。
2. 项目内存占用从传统方案的 1.2GB 降至 200MB~300MB。
3. 退出即焚，零残留孤儿进程。
