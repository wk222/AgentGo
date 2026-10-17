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

### D2 原生代码工具（3–5 天，收益最大）✅ 2026-10-17（D2a view/write/edit/multiedit + D2b grep/glob/ls；闭包收缩验收改挂 D3）
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

**实测（2026-10-17，D6 后）：** codetools 闭包 0 包；`./internal/...`、`./cmd/...` 闭包 0 包 / 0 行；AgentGo 非测试代码对 `third_party/crush` 的 import 点 = 0（仅 `-tags crushparity` 的对拍测试文件引用）；`third_party/crush` 磁盘 75 包 / 100,331 行保留作参照，删除待确认（D7）。`agentgo-cli` 的 `-v` 在全屏 TUI 下被忽略（日志只进 `agentgo.log`，避免 stderr 撕屏）。

数字是目标不是承诺；每阶段以 `crush-closure.py` 实测为准，写进 `docs/plan.md` 的阶段记录。

## 5. 风险与不做清单

- **LSP 是唯一硬骨头**：服务器进程管理、增量同步、诊断时序都有坑。所以放 D3，且 D2 先行（工具不依赖它也能替换大半）。
- **不要移植 Crush 的 UI 代码**：`ui/model`+`ui/chat`+`ui/dialog` 近 3 万行，移植等于养第二个产品。只对齐功能清单。
- **不要迁 Crush 的 config/provider 目录**：与 AgentGo 的 LLM 设置双轨运行正是当前缝合感来源之一，迁过来等于把双轨固化。
- **OAuth 默认丢弃**：登录态、回调服务器、令牌存储都是长期维护负担；要保留就做 plugin，隔离在内核之外。
- 每阶段若评测退化或对拍不绿，停在该阶段，不叠加下一阶段。

**D0 结果（2026-10-17）：** 已落地 `internal/arch/crush_freeze_test.go`（白名单只减不增，目前 5 个文件：tui 入口、bridge 3 处、codetools 适配器）、`scripts/crush-closure.py`（实测：磁盘 75 包 100,321 行；codetools 闭包 37 包 34,929 行；tui 闭包 73 包 100,199 行）、`internal/codetools/baseline_test.go`（view/edit/grep/write 真跑临时工作区 + 诊断非空断言，D2/D3 的原生实现必须过同一组断言）。

**D1 结果（2026-10-17）：** `internal/coderuntime` 新增原生 `Toolbox` 接口（`Tool`/`ToolResult`/`LSPClientInfo`/`LSPEvent`）；`internal/codetools.Wrap` 把 Crush 工具箱包装成该接口，`Adapt`/`RegisterTraits` 改吃 `coderuntime.Tool`；`AppService` 只持有接口，bridge 里只剩插件装配（`codetools_plugin.go` 的 `ct.New`/`ct.Options`）还碰 Crush 类型。冻结白名单从 5 个文件缩到 2 个（tui 入口 + 插件装配），codetools 包内仅适配器文件可引用。闭包数字不变（37 包 / 34,929 行），符合 D1 只解耦不删码的预期。

**D2a 结果（2026-10-17）：** internal/coderuntime 新增原生 view/write/edit/multiedit（`fs_tools.go`、`fs_edit.go`、`edit_match.go`，纯标准库，无 Crush 依赖），`internal/codetools.Overlay`/`NativeFS` 把它们叠在 Crush 工具箱上同名替换；开关 `AGENTGO_CODETOOLS_NATIVE`（默认开，`0` 回退 Crush 实现）。LSP 仍由 Crush 提供：写后诊断 = 调 `lsp_diagnostics`，view 只预热 LSP。对拍测试 `native_test.go` 用 28 步场景（先读后改、重复/缺失匹配、空白容错与提示、删除/创建、分页、multiedit 部分失败、CRLF 保持）在两套实现上比对模型可见输出和磁盘字节，全部一致；`baseline_test.go` 两套实现都过。
有意差异（4 处，均为小改进或去耦）：① multiedit 把每个失败编辑的原因写进结果（Crush 放在模型看不到的 metadata）；② view 不再附带诊断块（诊断用 `lsp_diagnostics`，省掉每次 300ms 等待）；③ 图片文件返回文本说明（Crush 要靠模型是否支持图片的上下文）；④ 读后改的追踪用内存表，不再落 SQLite（进程重启后需重新 view，与重启后会话本就丢失一致）。
闭包数字**未变**：`pkg/codetools` 仍构造 Crush 的七个文件工具（grep/glob/ls 还在用），要等 D2b 把 grep/glob/ls 也原生化、并让 `pkg/codetools` 只构造 LSP 工具后，`agent/tools`/`shell`/`skills`/`history`/`filetracker` 才会从闭包里消失。D2b 的难点是 `fsext` 的 gitignore 感知遍历和 Crush 自己的 rg 查找（可直接复用 `ideruntime` 内嵌的 `rg.exe`）。
**D2b 结果（2026-10-17）：** `internal/codetools` 新增原生 grep / glob / ls（`search.go`、`search_ls.go`、`search_walk.go`、`search_ignore.go`），不依赖 Crush 的 `fsext`：自己的有序遍历 + go-git gitignore 匹配（`.gitignore`/`.agentgoignore`/`.crushignore` 逐级叠加）+ doublestar。有 ripgrep 就用 rg（`ideruntime.RgBinary()`：Windows 内嵌版，否则 PATH；此前 Crush 只认 PATH），没有就退回纯 Go 遍历。生产装配改为 `ct.Options{LSPOnly: true}`（Crush 只构造 5 个 LSP 工具，不再创建 view/edit/…/ls 及其权限、历史、追踪状态）+ 原生工具叠加；`AGENTGO_CODETOOLS_NATIVE=0` 仍整体回退到 Crush 全套。
对拍：`TestNativeSearchParityWithCrush`（grep 字面/include/无匹配/非法正则/隐藏文件/gitignore，glob，ls 全量/depth/ignore/不存在路径）与 Crush 输出逐字一致；`TestNativeSearchRgMatchesWalker` 保证 rg 路径与 Go 遍历找到同一批文件；`TestNativeAssemblyKeepsToolSet` 保证生产装配的工具名集合与 Crush 全套相同。
有意差异（3 处）：① Crush 的 Go 回退 grep 会进 `node_modules`（目录用了"文件"规则，常见目录名表只对目录生效），原生不进（`TestNativeSearchSkipsNoiseDirs`）；② ls 的同级目录顺序：Crush 在 depth 限制下顺序不稳定，原生固定"先文件后目录、字典序"，测试按行排序后比对；③ ls 传 `depth` 参数时 Crush 不打印"深度提示"（它只看配置里的 depth），原生按参数打印。另：ls 对反斜杠根路径也能出树（Crush 在 Windows 上因路径分隔符不一致会把整条绝对路径嵌套成树）。
闭包数字**仍未变**（37 包 / ~34.9k 行）：Go 按包粒度编译，`agent/tools` 里的 LSP 工具（diagnostics/references/symbols/definition/callhierarchy）和 TUI 用的其它工具同在一个包里，Crush 的 view/edit/…/ls 源文件因 TUI 仍在用而不能从 vendored 树里删。真正的收缩要等 D3（把 5 个 LSP 工具连同 `lsp` 一起原生化）之后，`pkg/codetools` 才能整体删除，codetools 闭包才归零。D2 的"闭包消失"验收项因此改挂在 D3；D2 本身的验收（原生工具上线、对拍全绿、默认走原生）已满足。

**D3 结果（2026-10-17）：** 新包 `internal/codelsp`（`client.go` / `manager.go` / `tools.go` / `toolbox.go`），**没有按原计划手写 JSON-RPC**：Crush 的 `lsp` 本身只是 `charmbracelet/x/powernap` 的薄封装，原生层直接建在 powernap 上（协议面、默认服务器表、传输都来自它），Crush 的 `lsp`/`config`/`agent/tools` 不再参与。实现 5 个工具（`lsp_diagnostics/references/symbols/definition/call_hierarchy`）+ `coderuntime.Toolbox` 全部方法（状态表、事件监听、预热、有界关闭），生产装配 `bridge.newToolbox` 默认走 `codelsp` + 原生 FS/搜索（`AGENTGO_CODETOOLS_NATIVE=0` 回退 Crush 全套）。
对拍：`TestNativeLSPParityWithCrush` 用真 gopls（`GOPATH/bin` 自动加入 PATH，找不到则跳过）跑 symbols/诊断/定义/引用/调用层级（incoming+outgoing）/不存在符号/不存在文件，原生与 Crush 输出逐字一致，连跑 3 次稳定；codelsp 另有单测（lsp.json 解析、filetype/root-marker 选择、覆盖与禁用、"非标识符"判定、启动门）。
有意差异：① **启动竞态修复**：Crush 在服务器"启动中"时直接放行调用，首个 `lsp_*` 会撞上未初始化客户端（"client not initialized"，预热协程与首次调用并发时必现）；原生让后到者等待 `started` 门（受 initialize 超时约束）。对拍里 Crush 一侧必须先预热才有有效基线，原生不需要。② **符号候选**：Crush 取 grep 的第一个"不报错"候选，gopls 对注释处命中返回空结果而不报错，于是 `Add` 的定义/调用层级是否成功取决于文件 mtime 顺序；原生要求定义解析出至少一个位置，否则跳过候选继续找，结果不再依赖 mtime（对拍种子里手动钉死 mtime 才让 Crush 稳定）。③ 符号搜索由原生 `searcher` 在工作区根执行（Crush 从进程 cwd 起 grep，Wails 进程 cwd ≠ 工作区时会 "symbol not found"，对拍里需 `t.Chdir`）。④ **用户 LSP 配置迁移**：原生不再读 `crush.json` 的 `lsp` 段，改读 `<DataDir>/codetools/lsp.json`（`{"名字":{command,args,env,filetypes,root_markers,init_options,options,disabled,timeout}}`，同名覆盖内置、未知名新增、`disabled` 关闭；用户配置的服务器跳过 PATH 校验）。
**收尾说明：** `go list` 闭包数字仍未变（37 包 / ~34.9k 行）——不是原生没接上，而是 `AGENTGO_CODETOOLS_NATIVE=0` 回退路径和三套对拍测试仍 import `third_party/crush/pkg/codetools`。删掉回退即同时失去 Crush 基线，因此留一个观察期：原生默认运行稳定后，一步删除 `pkg/codetools` + 回退分支 + 对拍测试（保留原生自测），闭包归零。此步放 D3 收尾 / D7，需用户点头。同时顺带修了 `TestNativeFSParityWithCrush` 的偶发失败（staleness 报错里带文件 mtime，跨秒不一致，现按 ISO 时间归一化）。arch 层注册 `codelsp`=Assets 层。

**D4 结果（2026-10-17）：** 核对后 D4 的三项（permission→governance、config 拆分、shell 对齐）在 codetools 路径上已被 D2/D3 覆盖：Crush 工具箱只含文件/搜索/LSP 工具，这些全部原生化后，原生路径运行时从未碰 Crush 的 `config`/`permission`/`shell`；审批走 `FSRequest`+governance 风险表（`policy_gate_test.go` 现在对**原生**工具箱断言：变更类工具默认策略下必须停下审批、只读类不拦、工具名集合固定）。D4 实际剩下的只是"去掉回退"，按用户选择采用 build tag 而非整删：
- 生产代码去掉回退：`bridge.newToolbox` 只装配原生工具箱；删除开关 `AGENTGO_CODETOOLS_NATIVE`（设了也无效）。
- Crush 参照实现退到 build tag `crushparity` 下：`internal/codetools/wrap.go`（`Wrap`）与 `crush_parity_test.go`（FS/搜索/LSP 对拍、工具集一致性）；手动运行 `go test -tags crushparity ./internal/codetools`（本次实测全绿）。默认测试里 baseline/策略门/原生 LSP（真 gopls）仍照跑，对拍用例在无 tag 时 skip。
- 冻结白名单只剩 `cmd/agentgo-tui/main.go → embedtui`（`internal/codetools/` 的 tag 文件是唯一例外，不在任何默认构建里）。
- **闭包实测（`scripts/crush-closure.py`）：codetools 闭包 37 包 / 34.9k 行 → 0；`./internal/...` 对 Crush 的依赖 → 0**。仅剩 TUI 入口 72 包 / 99.7k 行（D6 处理）。
- `crush.json` 的 `lsp` 段不再被读取，迁移到 `<DataDir>/codetools/lsp.json`（见 D3）。
下一步 D5（裁剪决策，只决策不写码）→ D6（原生终端，需用户改判"冻结 tui/cli"）。

**D5 结果（2026-10-17）：** 裁剪决策已定（详见 `docs/plan-native-tui.md` §1）：`oauth/*`、`hooks`、`commands`、`update`、`herdr` 全部丢弃；`crushproto` 服务端 + `hostlink` + `crushproto-probe` + `agentgo-headless` 按用户决策丢弃（附着桌面宿主功能不再提供，会话仍共享 SQLite）。

**D6 结果（2026-10-17）：** 用户决策：解冻 `tui`/`cli`，新写 BubbleTea 全屏 TUI（不是增强行模式）。落地 `internal/tui`（时间线 + 流式 + glamour Markdown、审批浮层含 edit/multiedit/write 变更预览、提问、file_change diff、LSP 诊断着色与状态栏计数、会话选择器与历史重放、`/` 命令、取消/退出/缩放），`agentgo-cli` 在终端下默认进入，`-plain` 保留行模式。功能对齐清单全部打勾（见 `docs/plan-native-tui.md` §5/§8）。同时删除 Crush 侧入口与协议代码。**闭包实测：`./internal/...`、`./cmd/...` 对 `third_party/crush` 依赖 = 0；`cmd/agentgo-cli` 闭包 = 0。** 冻结白名单已清空。
剩 D7：`third_party/crush`（75 包 / 100,331 行）现在只被 `-tags crushparity` 的对拍测试引用，删除它即失去 Crush 参照基线（用户此前选择保留在 tag 之后），所以 D7 的整目录删除 + `go mod tidy` + 全新 clone 构建验证等用户确认再做。
**D7 结果（2026-10-17）：删除前先实测，再清场。**
实测（真模型 gpt-6-luna，同一份 Go 小工程夹具，9 个任务 × 3 次 × 2 套构建；A = 纯 Crush 版 `d647700` 的 `agentgo-cli`，B = 当前原生版；脚本 `scripts/ab_codetools.py`，每次运行用全新夹具副本、`-yolo`，按文件/答案自动判分）：

| 任务 | A（Crush） | B（原生） |
|---|---|---|
| 找函数定义（grep） | 3/3 | 3/3 |
| 改常量（view+edit） | 3/3 | 3/3 |
| 修 bug 并跑测试 | 3/3，均 5.7 次工具/29s | 3/3，均 3.3 次/20s |
| LSP 诊断 | **1/3**（两次答"无诊断"） | **3/3** |
| LSP 引用 | 3/3 | 3/3 |
| 读指定行 | 3/3 | 3/3 |
| 新建文件并编译 | 3/3 | 3/3 |
| 重命名并改调用方 | 3/3 | 3/3 |
| glob 列文件 | 3/3 | 3/3 |
| **合计** | **25/27** | **27/27** |

结论：大体一致，原生不差于 Crush。唯一差异是 LSP 诊断：Crush 在服务器"启动中"时放行调用，首个 `lsp_*` 撞上未初始化客户端而返回空（即 D3 记录的启动竞态，真模型下稳定复现），原生用启动门修掉。另一个已知差异：Crush 的 grep 从进程 cwd 起搜，A/B 脚本为公平把两边的 cwd 都设为工作区。工具层面另有 `-tags crushparity` 对拍（FS 28 步、搜索、LSP 8 步）在删除前最后一次全绿。
删除：`third_party/crush`（75 包 / 100,331 行）、`scripts/vendor-crush.py`、`scripts/crush-closure.py`、`internal/codetools/{wrap.go,crush_parity_test.go}`、`crushparity` 构建标签；`go mod tidy` 同步去掉约 40 个只被 Crush 用的直接依赖（go.mod −124/go.sum −233 行）。
为不丢掉对拍价值，删除前把 Crush 对 FS/搜索/LSP 场景的真实输出录成 `internal/codetools/testdata/crush_golden.json`（含 gopls 版本），原生测试 `TestNative*MatchesCrushRecording`/`TestNativeLSPToolsAgainstGopls` 逐字比对（LSP 仅在同版本 gopls 下比）。归属与许可证保留在 `licenses/crush/`（`NOTICE.md` + `LICENSE.md`，被派生文件的头注释指向它）；架构测试改为 `TestCrushIsNotImported`，禁止再次引入 Crush。

**D7 评测全量（删除后，同一模型 gpt-6-luna，`--runs 3`，对 `docs/eval/baseline.json`）：** 19/19 用例全过（57 次运行，311 秒），prompt token 803,144（基线 786,759，+2%），工具调用 106（基线 103）；缓存命中 83%（基线 94%，仅警告级，未定位原因，见下一步）。原始结果未入库（`docs/eval/baseline.json` 不更新：行为未有意改变）。

**缓存命中复查（2026-10-17）：不是回归，是噪声。** 评测里 94% → 83% 引起怀疑后，做了两件事：① 用 `AGENTGO_DUMP_LLM_DIR` 转储一轮评测的 57 个请求，按语义（不是按 JSON 字节，body 里 `messages` 排在 `tools` 前，字节前缀会误报）比对：同一次对话里系统消息和历史消息逐条稳定，唯一会变的是 `tool_search` 加载延迟工具时在 `tools` 末尾追加一个工具（既有设计，已知代价）。② 用同一评测各跑 3 轮、交替执行（A=Crush 版 `d647700`，B=当前原生版，`--runs 1`）：A 命中 94/94/93%，B 命中 89/97/98%，19/19 全过，工具调用数相同（35–37）。B 第一轮偏低像是缓存冷启动，之后追平并更高。结论：原生化没有破坏前缀稳定性。另记一笔：B 的 prompt token 总量比 A 高约 5%（276–286k vs 260–283k），工具调用数相同，未追查。
