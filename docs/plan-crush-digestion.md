# Crush 代码消化计划（Eino + AgentGo 现有组件逐步替代）

日期：2026-10-17。前置：Crush 源码已并入 `third_party/crush`（见其 `NOTICE.md`），全新 clone 可构建。

## 0. 现状盘点（实测）

`third_party/crush` 共 75 个包、**100,321 行**，但 AgentGo 真正的入口只有两个：

| 入口 | 闭包 | 行数 | 说明 |
|---|---|---|---|
| `internal/codetools`（经 `pkg/codetools`） | 37 包 | 34,929 | LSP + 代码工具，被 Eino 智能体循环使用 |
| `cmd/agentgo-tui`（经 `pkg/embedtui`） | 73 包 | 100,199 | 原版 Crush TUI 当终端身体，走它自己的 agent/config/db |

关键事实：

- **值得保留的能力只有两块**：LSP 客户端（`internal/lsp`+`util`，1,621 行）和代码工具实现（`internal/agent/tools`，7,989 行）。
- 其余都是**重复建设**：Crush 的 agent 循环、config、session/db、permission、shell、skills，AgentGo 都有对应物（Eino Runner、`internal/config`+LLM 设置、`internal/sessions`、`internal/governance`、`internal/shellcmd`+`ideruntime`、`internal/skills`）。
- TUI 相关（`internal/ui/**`、`app`、`backend`、`server`、`client`、`proto`、`workspace`）约 65,000 行，是大头，且**不该逐行移植**——应该用 AgentGo 自己的终端（`internal/cli` 1,800 行 + `internal/tui` 渲染层）接网关/ACP 后整体删除。

## 1. 原则

1. **不逐行搬运，按接口重写**。Crush 代码只当行为规格和测试用例来源；新实现写在 AgentGo 包里，用现有组件。
2. **绞杀式替换**：每个阶段先加原生实现并与旧实现并行跑对拍测试，切换后再删旧包。每阶段结束时 `third_party` 只能变小。
3. **冻结**：`third_party/crush` 内不接受新修改（同 NOTICE 规则）；AgentGo 侧新增对它的 import 一律禁止（D0 用架构测试锁死）。
4. **每阶段有验收门**：`go build ./...`、`go test ./internal/...`、评测基线不退化（`docs/eval-baseline.md`）、以及该阶段的对拍测试。
5. **删除要有闭包证据**：用 `scripts/crush-closure.py`（D0 产出）证明某包已不在任何闭包里再删，不靠感觉。

## 2. 目标映射

| Crush 资产 | AgentGo 替代物 | 处理 |
|---|---|---|
| `agent/tools` 的 view/edit/multiedit/write | `internal/workspace` + `internal/codetools`（原生 Eino 工具） | 重写（D2） |
| grep/glob/ls | `internal/ideruntime` SearchService（已内嵌 ripgrep）+ WorkspaceFS | 重写（D2） |
| bash / jobs | 已有 `execute_bash`（`internal/tools`）+ `ideruntime.TerminalService` | 对齐语义后删（D2） |
| diagnostics/references/symbols/definition/call_hierarchy | 原生 LSP 客户端（D3） | 先隔离后重写 |
| `internal/lsp` | `internal/coderuntime` 下的原生 LSP（JSON-RPC over stdio，标准库） | 重写（D3），最后做、最难 |
| `permission` | `internal/governance`（已有审批队列+策略） | 删适配层（D4） |
| `config`（5459 行） | 模型目录→`agent` 的 LLM 设置；LSP 配置→coderuntime 配置；MCP 配置→`pkg/mcphost`/plugin；其余→`internal/config` 注册表 | 拆分吸收（D4） |
| `session`/`db`/`message`/`history` | `internal/sessions` + `internal/core` transcript | 随 TUI 一起删（D6） |
| `shell`/`shellconfig` | `internal/shellcmd`、`internal/sandbox`、`ideruntime` PTY | 对齐后删（D4） |
| `skills` | `internal/skills` | 删（D4） |
| `pubsub`/`event`/`csync`/`lock` | `internal/event`、`internal/capability` Bus、标准库 channel | 随用随删 |
| `oauth/*`、`discover`、`hooks`、`update`、`herdr`、`commands` | 无对应产品需求 | **默认丢弃**（D5 单独裁决，OAuth 若要保留做成独立 plugin，不进内核） |
| `ui/**`、`app`、`backend`、`server`、`client`、`proto` | `internal/cli`+`internal/tui` 经网关/ACP 接 `core` | 整体删除（D6） |
| 小工具包（fsext/filetracker/diff/format/gitutil/home/env/…） | 就近内联到使用方 | 随阶段顺手删，不单独立项 |

## 3. 分阶段

### D0 冻结与基线（0.5–1 天）✅ 2026-10-17
- 架构测试：`third_party/crush` 只允许被 `internal/codetools`、`internal/bridge`（现有 3 处）、`cmd/agentgo-tui` import；新增引用直接红。
- 写 `scripts/crush-closure.py`：输出各入口闭包的包数/行数，作为每阶段的进度指标。
- 给 codetools 建行为基线：现有工具的输入/输出快照测试（编辑、搜索、诊断各至少一条），作为 D2/D3 的对拍标准。
- **验收**：基线测试全绿；指标脚本能复现 37 包 / 34,929 行 与 73 包 / 100,199 行。

### D1 codetools 接口收口（1–2 天）✅ 2026-10-17
- 在 `internal/coderuntime` 定义 AgentGo 自己的 `Toolbox` 接口（Tools、LSPStates、LSPDiagnostics、StartLSP、事件监听），`bridge` 和 `core.ExpandMentions` 只依赖接口。
- 现有 Crush 实现作为接口的第一个实现者（适配器），行为不变。
- **验收**：`bridge` 中除适配器装配外不再出现 `ct.` 类型；测试不变全绿。删 0 行，但解耦完成。

### D2 原生代码工具（3–5 天，收益最大）
- 用 `workspace`+`ideruntime` 实现 view/edit/multiedit/write/grep/glob/ls 七个工具（Eino 工具，经现有 `internal/codetools` 适配层注册），语义对齐 Crush：编辑前快照、文件追踪、skills 路径提示等行为逐条对拍。
- bash 对齐 `execute_bash`；todos/question 分别对齐已有 taskhub/`ask_user`，不新写。
- 与 Crush 实现并行注册为影子工具跑对拍（同参数同工作区，比输出），全绿后切换，`pkg/codetools` 改只剩 LSP 工具。
- **验收**：评测集（含写代码→跑测试类用例）不退化；`go list -deps ./internal/codetools` 中 `agent/tools`、`tools/mcp`、`shell`、`skills`、`question`、`history`、`filetracker` 消失。预计闭包 −12,000 行左右。

### D3 原生 LSP（1–2 周，最难，放后面）
- 先把 Crush LSP 的协议面盘清：initialize、didOpen/didChange、publishDiagnostics、definition、references、documentSymbol、callHierarchy、rename。
- 用标准库 JSON-RPC over stdio 在 `internal/coderuntime` 写最小客户端；服务器发现与启动配置从 Crush `config` 的 LSP 段迁成 AgentGo 配置结构。
- 对拍：同一仓库（agentgo 自身）跑 gopls，比诊断条数与定义跳转结果。
- **验收**：`lsp`、`lsp/util`、`discover` 从闭包消失；`pkg/codetools` 可删。预计 −3,000 行，且去掉一台“服务器生命周期”状态机。

### D4 基础设施替换（3–5 天）
- permission → governance：原生工具直接走已有治理中间件，删 codetools 的 Approver 桥与 Crush `permission`/`pubsub`。
- config 拆分吸收（见映射表），Crush 的 provider/model 目录**不迁**（AgentGo 用自己的 LLM 设置与 Eino 模型工厂）。
- shell → shellcmd/sandbox/PTY 对齐（超时、进程树终止、输出截断三处语义对拍）。
- **验收**：`config`、`permission`、`shell`、`shellconfig`、`db`、`session`、`message`（若 TUI 未删则此项顺延 D6）从 codetools 闭包消失。预计 codetools 闭包降到只剩接口+原生实现，`third_party` 对桌面/网关入口为 0。

### D5 裁剪决策（0.5 天，只决策不写码）
- 对 `oauth/*`（Copilot/Grok/OpenAI 登录）、`hooks`、`commands`、`update`、`herdr` 逐个裁决：丢弃 / 做成独立 plugin / 保留在 TUI。
- 默认丢弃；任何“保留”必须有产品理由并单独排期，不阻塞 D6。

### D6 原生终端替代 TUI（1–2 周，独立立项）
- 前置决策：终端形态选 `internal/cli`（现有 REPL）增强，还是新写 BubbleTea 界面。此项**解冻**此前冻结的 tui/cli 入口（与阶段 F 的冻结决定冲突，需明确改判）。
- 功能对齐清单：流式输出、审批交互、diff 展示、LSP 诊断提示、会话切换、`/` 命令。每项对齐后打勾。
- 切换后删除 `pkg/embedtui`、`pkg/codetools`（若 D3 未完则保留 LSP 部分）、`ui/**`、`app`、`backend`、`server`、`client`、`proto`、`workspace`、`agent/**`。
- **验收**：`cmd/agentgo-tui` 不再 import `third_party`；预计 −65,000 行。

### D7 清场（1 天）
- 删剩余小工具包与 `third_party/crush` 整个目录；`go.mod` 无 crush 残留；NOTICE 改为历史记录或随目录删除。
- 全新 clone 构建验证 + 评测全量跑一遍。
- **验收**：`rg third_party/crush internal cmd` 为 0；仓库行数与构建时间对比记录在案。

## 4. 进度指标（每阶段更新）

| 指标 | 现在 | D2 后（目标） | D4 后（目标） | D7 |
|---|---|---|---|---|
| codetools 闭包包数 | 37 | ~20 | 0（原生） | 0 |
| 全部闭包行数 | 100,321 | ~85,000 | ~68,000 | 0 |
| AgentGo 直接 import 点 | 5 文件 | 2（接口装配） | 1 | 0 |

数字是目标不是承诺；每阶段以 `crush-closure.py` 实测为准，写进 `docs/plan.md` 的阶段记录。

## 5. 风险与不做清单

- **LSP 是唯一硬骨头**：服务器进程管理、增量同步、诊断时序都有坑。所以放 D3，且 D2 先行（工具不依赖它也能替换大半）。
- **不要移植 Crush 的 UI 代码**：`ui/model`+`ui/chat`+`ui/dialog` 近 3 万行，移植等于养第二个产品。只对齐功能清单。
- **不要迁 Crush 的 config/provider 目录**：与 AgentGo 的 LLM 设置双轨运行正是当前缝合感来源之一，迁过来等于把双轨固化。
- **OAuth 默认丢弃**：登录态、回调服务器、令牌存储都是长期维护负担；要保留就做 plugin，隔离在内核之外。
- 每阶段若评测退化或对拍不绿，停在该阶段，不叠加下一阶段。

**D0 结果（2026-10-17）：** 已落地 `internal/arch/crush_freeze_test.go`（白名单只减不增，目前 5 个文件：tui 入口、bridge 3 处、codetools 适配器）、`scripts/crush-closure.py`（实测：磁盘 75 包 100,321 行；codetools 闭包 37 包 34,929 行；tui 闭包 73 包 100,199 行）、`internal/codetools/baseline_test.go`（view/edit/grep/write 真跑临时工作区 + 诊断非空断言，D2/D3 的原生实现必须过同一组断言）。

**D1 结果（2026-10-17）：** `internal/coderuntime` 新增原生 `Toolbox` 接口（`Tool`/`ToolResult`/`LSPClientInfo`/`LSPEvent`）；`internal/codetools.Wrap` 把 Crush 工具箱包装成该接口，`Adapt`/`RegisterTraits` 改吃 `coderuntime.Tool`；`AppService` 只持有接口，bridge 里只剩插件装配（`codetools_plugin.go` 的 `ct.New`/`ct.Options`）还碰 Crush 类型。冻结白名单从 5 个文件缩到 2 个（tui 入口 + 插件装配），codetools 包内仅适配器文件可引用。闭包数字不变（37 包 / 34,929 行），符合 D1 只解耦不删码的预期。
