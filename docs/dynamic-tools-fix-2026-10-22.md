# 动态工具（自扩展）一致性修复记录

> 日期：2026-10-22 晚
> 背景：项目审查发现工具生成存在「已生成 ≠ 可运行」的状态不一致，外加并发串参、注册错误静默丢失。本文记录本次修复范围、设计与未做事项。

## 修前的问题（均有代码依据）

| # | 问题 | 位置（修前） |
|---|------|--------------|
| 1 | 无效 Python 也报「compiled and registered」：全仓无 `py_compile`/`ast.parse` 校验，所谓编译只做参数 JSON 解析和构造工具对象 | `internal/tools/creator.go`、`registry_dynamic.go` |
| 2 | 注册失败留僵尸定义：先落库后注册，失败只回一句 `saved but compile failed`，定义留在库里；重启恢复时静默 `continue` 跳过，无日志 | `creator.go:50-59`、`registry_dynamic.go:35-47` |
| 3 | 并发串参：参数/wrapper 文件固定名 `<工具名>_args.json` / `<工具名>_wrapper.py`，同工具并发调用互相覆盖 | `dynamic_tool.go:62,81`、`python_exec.go:51,76,105` |
| 4 | 桥接层注册错误普遍 `_ =` 吞掉，功能静默缺失无从排查 | `internal/bridge/runtime_plugins_agent.go` 多处 |
| 5 | 扩展包安装同病：落库后注册失败被 `_ =` 吞掉，仍计入「已安装」 | `internal/agentpack/agentpack.go:361` |

## 本次修复

### 顺序原则：校验 → 落库 → 注册，失败回滚

- `internal/tools/dynamic_tool.go`
  - 新增 `ValidatePythonSyntax`：用解释器 compile 只编译不执行（代码经 stdin 传入），15 秒超时；空代码视为合法（走内置 stub）。
  - 新增 `ValidateDynamicToolDef`：参数 JSON + Python 语法 + 工具构造/名字合法性，一次全验。
  - 新增 `pythonInterpreter` 统一解释器查找（Windows 优先 `python`）。
  - 并发修复：每次调用 `os.MkdirTemp(sandboxDir, <工具名>-run-)` 建独立目录，脚本/参数/wrapper 都放里面，结束 `RemoveAll`；进程 cwd 仍保持沙箱根目录，避免破坏依赖相对路径的老工具。
- `internal/tools/registry_dynamic.go`
  - `RegisterDynamicTool` 注册前强制 `ValidateDynamicToolDef`。
  - `SyncDynamicFromStore` 恢复失败不再静默：逐个打日志（工具名 + 原因 + 「定义保留在库中，修复或删除」），结尾汇总 restored/skipped 数；校验在锁外执行，避免启动时长时间占注册表锁。
- `internal/tools/creator.go` / `tool_template.go`
  - 流程改为：先校验（不过直接拒绝、不落库）→ 快照旧定义 → 落库 → 注册 → 失败回滚（新工具删除、更新工具恢复旧版）→ 成功后才发能力注册。
  - 成功文案改为如实描述：`validated (parameters + Python syntax) and registered ... Registration is not a functional test`，不再冒充「已编译」。
  - `create_tool` 的 code 字段说明改正：原写「stored, not auto-executed」，实际每次调用都在本地 Python 子进程执行，现已写明。
- `internal/tools/dynamic_store.go`：新增 `Delete` 供回滚。
- `internal/tools/python_exec.go`：老式 `execute_dynamic_tool` 同步改每次调用独立目录（含 Docker 分支的容器内路径）。
- `internal/agentpack/agentpack.go`：扩展包内工具注册失败时回滚落库并 `skip` 记录，不再计入已安装。
- `internal/bridge/runtime_plugins_agent.go`：引入 `noteReg` 收集器，主要工具族注册失败逐条 `applog.Warn` 并在结尾汇总计数；不再 `_ =` 静默吞。

### 回归测试（新增 `internal/tools/dynamic_tool_validation_test.go`）

| 测试 | 验证点 |
|------|--------|
| `TestValidatePythonSyntaxRejectsInvalidCode` | 语法错误被拒、合法代码通过 |
| `TestCreateToolInvalidPythonIsNotPersisted` | 无效 Python 创建失败且库里无残留、未注册 |
| `TestCreateToolRegistrationFailureRollsBack` | 注册失败时新工具不落库、更新工具恢复旧版定义 |
| `TestDynamicToolConcurrentInvocationsDoNotShareArgs` | 12 并发同工具调用各自拿到自己的参数 |
| `TestSyncDynamicFromStoreSkipsInvalidButKeepsValid` | 启动恢复跳过坏定义并保留其库内记录，正常定义恢复 |

## 第二批（同日晚补）：环境隔离

参考 OpenCode（无 OS 沙箱、仅审批）与 Cursor（审批 + OS 沙箱；macOS Seatbelt、Linux Landlock+seccomp、Windows 借 WSL2）的做法后落地：

- **环境变量白名单**（`dynamic_tool.go` 的 `buildToolEnv`）：工具进程只拿到基础变量（PATH、TEMP、USERPROFILE、PYTHONUTF8 等），宿主密钥类变量（各种 API Key、Token，例如 `AGENTGO_GATEWAY_TOKEN`）默认不传。工具确实需要的，在 `create_tool` 时用新字段 `env_vars` 声明（逗号分隔，落库保存），运行时按声明放行——创建工具本身是 High 审批，声明是可审查的。老式 `execute_dynamic_tool` 同步生效。
- **Docker 分支接通主路径**：`runPythonToolBody` 在 `SandboxDocker` 开启且守护进程可达时与 `python_exec.go` 走同一容器路径（只带声明变量 + 参数文件路径）；开启但 Docker 不可达时打日志并降级为本地过滤环境执行，不再静默裸跑。
- 存储层 `dynamic_tools` 表新增 `env_vars` 列（迁移自动补，老库兼容），扩展包导入导出自动携带。
- 新增测试：`TestBuildToolEnvFiltersSecretsUnlessDeclared`（未声明的密钥不泄露、声明的放行）、`TestEnvVarsPersistThroughCreateAndStore`（声明经 create_tool 往返落库）。

**实事求是的边界**：本地执行仍以当前用户权限运行、能读写整个磁盘、能联网——环境过滤堵的是「顺手偷 Key」，不是「代码隔离」。真隔离只有 Docker 一条路（内网机未必装），这点与 Cursor 在 Windows 上借 WSL2 的困境相同；无 Docker 时界面与文案不应假装有沙箱。

## 第三批：审查后的校验与回滚补漏

- 语法校验使用 Python -I -S，禁止从当前目录、PYTHONPATH 或 site 启动钩子加载用户代码；子进程采用基础环境白名单，不传工具声明的密钥。即使执行阶段选用 Docker，宿主校验阶段也遵守此规则。
- 改用 compile(source, filename, "exec")，不执行生成代码，同时拒绝顶层 return、break、continue 等 AST 可解析但不能执行的代码。
- 扩展包导入在落库前完整校验，包括没有实时注册表的导入场景。覆盖前保存旧定义，注册失败恢复旧定义；新工具失败才删除。回滚失败会明确返回原因。
- 回归测试覆盖启动钩子与工具正文均不执行、编译期错误拒绝，以及非法导入、注册失败、无注册表导入时旧工具及其环境声明保留。

## 后续事项状态

1. **输出上限已补**：动态工具两条执行路径和 Docker 捕获输出每路最多保留 1 MiB，超限明确标记并继续排空管道；仍保留 90 秒硬超时。
2. **工作区回退已补**：无效工作区回退到独立的 `dataDir/workspace` 子目录；切换时扩展引擎也同步新目录。`NormalizeRoot` 的过宽路径策略仍可细化。
3. **IPC 契约对账**：前端 `contract.ts` 手写签名与 Go 绑定无自动校验，`ListAdminTasks` 漂移是先例，建议加对账测试。
4. **上下文已专项优化**：增加规则内容及记忆召回缓存、分层压缩与恢复检查点，避免重复注入；未变化的发送前快照复用内容。首次扫描耗时仍需实测。详见 `context-compression-2026-10-22.md`。

## 附：同日上午已修（不在本次范围）

- fsnotify 监听死锁：`Watcher.Add` 持锁阻塞导致 600 秒超时，已改为锁外注册 + 事件协程异步遍历，测试由 600s 超时变为 0.58s 通过。
- 私有兼容层：自动绑定 LLM 的 binder 曾因不存在的 `UpdateLLMConfig` 永远失效，已改走 `AppService.SetLLMConfig`；代理改固定端口 `17456`；登录态移出工作区到数据目录。
