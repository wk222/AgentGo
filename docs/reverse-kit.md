# agentgo-re：自有逆向 MCP（`re_*` 工具集）设计与经验

> 日期：2026-10-21　状态：v0.1.0 已可用（`go test ./internal/rekit` 全绿，已在真实 APK/DLL/HAR 上冒烟）
> 参考库本地副本：`D:\youxiyanjiu\refs\{open-reverselab,rea,recurse,reagent}`（浅克隆，只读参考）

## 0. 一句话结论

四个库里**没有一个直接适合接进 agentgo**（语言/授权/体量都不对），但各自有一块值得偷的思路：

| 库 | 语言 / 授权 | 本质 | 我们采纳的 | 不采纳的 |
| --- | --- | --- | --- | --- |
| [morluto/rea](https://github.com/morluto/rea) | TypeScript / MIT | 面向多种目标（原生、Electron、.NET、APK、HAR…）的**统一 MCP + CLI**，Hopper/Ghidra/IDA 做后端 | **证据分级**（观察/推导/推断/未知）、所有答案分页+带局限说明、token 优先于一切的接口设计、"不确定就 fail-closed" | 48 个 provider 的庞大体量；Node 22+ 依赖 |
| [Recurse-Labs/recurse](https://github.com/Recurse-Labs/recurse) | Rust(Tauri) / Apache-2.0 | "逆向版 Cursor"桌面端；纯 Rust 静态引擎（capstone+VTIL 去虚拟化+反编译）；`recurse-mcp` 仅 ~450 行 | **一个后端无关接口**、按需(lazy)分析、SQLite 持久记忆（改名/发现跨会话保留） | 整个桌面端与自研反编译器（15k 行 `recurse-static`） |
| [Dryxio/reagent](https://github.com/Dryxio/reagent) | Python / MIT | **自治**函数还原：reverser→checker→修复循环，候选代码过"构建+测试+结构校验+parity(GREEN/YELLOW/RED)"四道门 | **"门控"概念**：高置信结论必须挂一个客观检查（本库的 `gate` 字段）；候选只写 overlay 不动原工程 | 全自动 LLM 循环（先把工具层做扎实）；依赖 ghidra-bridge |
| [LING71671/open-reverselab](https://github.com/LING71671/open-reverselab) | Python+Markdown / **GPL-3.0** | "可执行知识库"：180+ 篇攻击面文章 + `reverse_lab_tools` MCP（`@mcp.tool` 共 109 个，集中在一个 ~1750 行的 .py） | **目录即约定**（samples/exports/reports）、环境探测、上下文链 `CLAUDE.md→AGENTS.md→AI-USAGE.md` | **不拷贝任何代码**（GPL-3.0 会传染）；109 个工具的"工具菜单"式接口对模型是负担 |

> 你的 Cursor 里已挂着 `user-reverse_lab_tools`，其工具名（`triage_pe`、`ghidra_headless_analyze`、`sample_full_workup`、`kb_router`…）与 open-reverselab README 描述吻合，所以"装 open-reverselab"这件事你实际上已经有了。`agentgo-re` 定位是**补它做不到的**：证据账本、HAR 优先、XOR 掩码识别、APK/Unity 专项，并且是 Go 单文件二进制、能被 agentgo 自己的 MCP 加载器直接挂载。

## 1. 为什么这样设计：从你最近的项目反推需求

对 `yingzhiren/` 里的笔记（`REVERSE_ANALYSIS_AND_PLAN.md`、`MIGRATION_2017_4_AUDIT.md`、`ARM64_2K_APK_NOTES.md`、`webtrn_reverse_summary.md`、`yxlearning_reverse_summary.md`）归纳，你反复在做的是同几件事：

1. **APK/Unity 定性**：Mono 还是 IL2CPP、有无壳、Managed DLL 是否被改（1.3.0 的 `Assembly-CSharp.dll` 头 `B2 A5` = `MZ ^ 0xFF`，你当时靠反汇编 `libmono.so` 才发现）。→ `re_apk_inspect` + `re_xor_probe`
2. **HAR 优先的协议逆向**（网梯、大众云学：`sv.gson/cv.gson` 计时、AES-ECB `learnspaceaes123`、频次/倍速风控）。→ `re_har_summary` / `re_har_entry`
3. **长链路结论靠人脑维护**：每次都新开一个 `*_summary.md`。→ 案件账本 `re_evidence_*` + `re_report`
4. **外部工具一堆、路径各异**（ilspycmd 在 `E:\tools\ilspy`、IDA 在 `E:\Program Files`…）。→ `re_env` + `re_tool_run` 白名单
5. **大样本**（2.5GB arm64 APK）。→ 只读 zip 中央目录，实测 0.1 秒出结果

## 2. 工具清单（13 个，刻意不做"一个操作一个工具"）

| 工具 | 作用 | 备注 |
| --- | --- | --- |
| `re_env` | 列出可调用的外部工具与路径 | 找不到时给出配置方式（环境变量 `RE_TOOL_<NAME>` 或 `<home>\tools.json`） |
| `re_triage` | 任意文件：哈希、熵、格式、节区、导入、.NET/壳/XOR 掩码提示 | 超大文件可 `max_hash_mb`，并在 `limits` 里声明哈希不是全文件 |
| `re_apk_inspect` | APK/XAPK：清单(包名/版本/SDK/权限/启动 Activity)、dex、ABI、引擎、壳特征、被 XOR 的 Managed DLL | 自带 AXML 解析，无需 aapt；XAPK 会进入内层 apk |
| `re_strings` | ASCII/UTF-16LE 字符串+文件偏移，正则过滤，分页，64MiB 窗口 | 返回 `next_scan_offset` 续扫 |
| `re_xor_probe` | 单字节 XOR 全键探测（MZ+PE 校验、ELF、zip、dex、Lua、UnityFS、IL2CPP 元数据…）；已知明文推导多字节密钥周期；解码**副本** | 绝不覆盖源文件 |
| `re_har_summary` / `re_har_entry` | 按 `METHOD host/path{id}` 聚合接口、query/body JSON 键、签名类请求头名；单条详情 | Authorization/Cookie 等默认**打码**，`reveal=true` 才明文 |
| `re_ghidra_export` | **Lazy + 持久二进制索引包**：将函数表/C反编译/导入/导出/字符串/调用图落盘到 `<case>/ghidra/<sha16>/index.json`，支持 `overview`/`functions`/`function_detail`/`imports`/`exports`/`strings`/`xrefs` 按需分页 | 自动生成 `AgentgoExport.java` 调 `analyzeHeadless`；无 Ghidra 时自适应降级 Rizin 或纯 Go 静态分析（PE `.pdata` 函数边界+导出表+`E8` 相对调用图 / ELF 符号表） |
| `re_gate` | **可执行客观门控引擎**：执行 `hash_eq` / `format_valid` / `byte_parity` / `json_golden`（含数值误差容限 `abs_tol`），写入 `<case>/gates.jsonl` 并返回 `gate-XXXX [PASS/FAIL]` | `re_evidence_add` 提交高置信推断时若引用 `gate-XXXX`，会自动强校验该门控必须存在且为 `PASS` |
| `re_evidence_add` / `re_evidence_list` / `re_report` | 案件账本（JSONL）与 `REPORT.md` | 规则见下；报告第五节自动输出客观门控核验记录 |
| `re_tool_run` | 白名单外部工具（ghidra-headless/jadx/apktool/ilspycmd/rizin/r2/idat/objdump/diec/capa/7z；adb、frida 需 `--allow-dynamic`） | argv 数组、无 shell、超时、输出落盘只回头尾；无 python/dotnet 等解释器 |

### 账本规则（来自 rea 的分级 + reagent 的门控）

- `observation` 必须有 `source`（哪个工具/文件/偏移），否则拒收。
- `derivation` 必须有 `basis`（依据的证据 id），没有依据就只能叫推断。
- `inference` 若声称 `confidence=high`，必须有 `gate`（客观检查，如"解码后 sha256 与既有文件逐字节一致""diff_exec 3205/3205"）。
- `supersedes` 取代旧记录，旧记录默认不出现在列表/报告里；`unknown` 带 `next_step`，报告第四节即"下一步"。

## 3. 验证实录（2026-10-21，均为真实文件）

- `m.Sgame.yzr_1.3.0…apk`：`re_apk_inspect` **自动**指出 `assets/bin/Data/Managed/Assembly-CSharp.dll` 头 `b2a56fff…`、单字节假设 `0xFF`；`re_xor_probe` 解码后 SHA-256 = `cbd4f4f7…6263`，与你之前手工得到的 `Assembly-CSharp.orig.dll` **逐字节相同**。
- `yingzhiren_v1.17.0_arm64_2k_mod.apk`（2.5GB）：包名 `m.Sgame.yzr`、versionCode 38、arm64、`Unity IL2CPP`，约 0.1 秒。
- `E:\2026-10-20-210112.har`（大众云学）：`re_har_summary` 直接聚合出 `/train/class/*.gson` 等接口及 `accountid/cookie` 请求头；**该文件带 UTF-8 BOM，初版解析失败，已修并加回归测试**。
- `ilspycmd` 首次运行失败："You must install or update .NET"（它要 .NET 6，机器只有更新的）。已改为该工具默认带 `DOTNET_ROLL_FORWARD=Major`，之后 2.3 秒列出全部类，输出落盘 53KB、只回传 700 字。
- 案件 `yzr` 已写入 3 条账本并生成 `D:\youxiyanjiu\re_cases\cases\yzr\REPORT.md`。

## 4. 使用

```powershell
cd C:\Users\user\Documents\GitHub\agentgo
go build -o bin\agentgo-re.exe ./cmd/agentgo-re      # GOCACHE 已在 D:\dev\go，不占 C 盘
go test ./internal/rekit
python scripts\re_smoke.py list --home D:\youxiyanjiu\re_cases
python scripts\re_smoke.py re_apk_inspect path=D:/x/game.apk --home D:\youxiyanjiu\re_cases
```

挂载方式（双通道：内置插件 + 独立 MCP Server）：

1. **AgentGo 内置 `rekit` 插件 + `reverse` Harness（零配置开箱即用）**：
   - `internal/bridge/rekit_plugin.go` 通过 `mcp-go` 进程内客户端将全部 11 个 `re_*` 工具直接注册进 AgentGo 的 `tools.Registry`、能力总线（`capability.Bus`）与治理风险表（7 个只读工具 `LOW`，4 个写/外部执行工具 `MEDIUM`）。
   - 新增第 5 个预设 Harness **`reverse`（逆向 Reverse，`ModeReverse × CanvasBalanced`）**：
     - 桌面 UI、HTTP Gateway、CLI 与 ACP（Zed 编辑器桥接）通过 `ListHarnesses` / `SetHarness("reverse")`（含别名 `re`、`reverse-engineering`）统一可见。
     - **Schema 隔离**：`re_*` 工具仅在 `ModeReverse`（及 `ModeAdmin`）下进入静态常驻工具表，避免在日常对话（`minimal`/`standard`/`creative`）中消耗约 2,500 tokens 的工具 Schema。
     - **只读画布门控**：在 `ModeReverse × CanvasFocused` 下自动隐藏 4 个会产生副作用的工具（`re_xor_probe`、`re_evidence_add`、`re_report`、`re_tool_run`），保留 7 个纯静态只读探查工具。
     - **系统提示词契约**：`ModeHints()` 在 `ModeReverse` 下自动注入 `[Reverse Harness Contract]`（四级认知阶梯、静态优先探查顺序、严禁原位覆写样本）。
   - 默认案件目录跟随当前工作区 `<workspace>/.agentgo-re`（切换工作区自动跟随，无需重载插件），也可通过 `AGENTGO_RE_HOME=D:\youxiyanjiu\re_cases` 全局指定（避开 C 盘）。
2. **外部 MCP 客户端挂载（Cursor / Claude Code 等）**：
   - `configs/agentgo-re.mcp.json` 已写好，复制其中的 `command/args` 到 Cursor 或 Claude Code 的 MCP 配置即可。
   - 工作流提示词同步沉淀在 `skills/reverse-engineering/SKILL.md`。

## 5. 已知限制（不要把这些当结论）

- XOR 只做**单字节**全键探测；多字节密钥需要你给 `expected_hex`（已知明文）。
- `re_strings` 的 UTF-16 只扫 2 字节对齐，跨窗口边界的字符串可能被截断。
- 壳/引擎判断来自**压缩包内文件名**，不是行为证据；`re_apk_inspect` 不执行任何代码。
- `re_triage` 对 ELF/Mach-O 只做头部级解析，不替代 Ghidra/IDA。
- ghidra-headless 的路径本机未找到（`re_env` 会标 `found:false`），需要你用 `RE_TOOL_GHIDRA_HEADLESS` 或 `<home>\tools.json` 指定。
- 账本是单进程追加写（带进程内互斥）；多个 agentgo-re 同时写同一案件不保证 id 不冲突。

## 6. 下一步（按价值排序）

1. **[已完成] ACP / 全局 "逆向 Harness"（`reverse`）**：已在 `internal/agent/{mode.go,harness.go,mode_policy.go}`、`internal/bridge/rekit_plugin.go` 与 `frontend/src/shared/harness.ts` 全链路打通，通过进程内 MCP 零子进程开销暴露 13 个 `re_*` 工具，并完成架构守卫与目录守卫测试闭环。
2. **[已完成] Ghidra headless 一键导出与持久惰性索引（`re_ghidra_export`）**：落地于 `internal/rekit/ghidra.go`。按样本 `sha256` 缓存在 `<case>/ghidra/<sha16>/index.json`，自动写出零 Python 依赖的 `AgentgoExport.java`，并在未安装 Ghidra 时自动回退到 Rizin 或纯 Go 静态分析（解析 x64 PE `.pdata` 异常表函数边界、PE 导出表、`0xE8` 相对调用图与 ELF 符号表），提供 7 种分页按需视图。
3. **[已完成] 门控化（`re_gate` + 账本强校验）**：落地于 `internal/rekit/gate.go`。支持 `hash_eq`、`format_valid`、`byte_parity`、`json_golden`（含浮点容差对拍 AS3/AIR `golden.json`），产出持久化 `gate-XXXX` 回执，并在 `re_evidence_add` 中强制核验 `confidence=high` 所引用的 `gate-XXXX` 必须为 `PASS`。
4. **[已完成] AgentGo 自主模块化自扩展底座（Self-Extension & Universal Adaptation）**：
   - **MCP 自主生成与热挂载（`manage_mcp_server`，`internal/tools/mcp_manager.go`）**：支持 `scaffold`（在工作区生成零依赖 stdio MCP Server 模板）、`register`（运行期免重启热连接新 MCP Server 并将其工具实时注入 `Registry` + 持久化到 `mcp_servers.json`）、`remove` 与 `list`。
   - **模块化渠道中心（`manage_channel`，`internal/channels/hub.go` + `internal/tools/channel_tools.go`）**：支持运行期动态注册/移除/发送 `feishu`（含 v2 HMAC-SHA256 签名）、`dingtalk`（含 URL 签名）、`wecom`、`telegram`、`webhook`（含 `X-AgentGo-Signature`）及入站 Webhook 报文归一化解析。
   - **万物自适配中枢（`manage_adapter`，`internal/tools/adapter_manager.go`）**：将自适配从工具底座扩展到四大维度：
     1. `file_parser`（`.agentgo/parsers.json`）：给内置 `parse_file` 动态挂载任意专有格式（`.sqlite`、`.pcap`、`.parquet`、`.bin`、`.swf` 等）的外部解码脚本。
     2. `cli_tool`（`.agentgo/cli_adapters.json`）：通过 `{{param}}` 参数模板将本机任意 CLI 命令零代码包装为标准 Eino Tool 并热注入 `Registry`。
     3. `lsp`（`<workspace>/lsp.json`）：自动 `probe` 本机语言服务器并写入 `codelsp` 覆盖配置，让 `lsp_definition`/`lsp_references`/`lsp_diagnostics` 自动适配 Haxe、Lua、AS3、Rust 等语言。
     4. `llm_profile`（`.agentgo/llm_profiles.json`）：按任务角色（`vision`/`local_ollama`/`fast_extract`）动态配置与 `probe` 探测模型端点。
   - **技能闭环落盘与元技能（`evolve_skill` + `skills/self-evolution/SKILL.md`）**：在 `evolve_skill` 中新增 `save` 与 `list` 动作，直接落盘 `<workspace>/skills/<name>/SKILL.md`，并将“发现缺口 → 选择适配层 → 编写与自测 → 热挂载 → 沉淀 `SKILL.md`”固化为 `skill:self-evolution` 元技能。
5. **IL2CPP 元数据结构深度解析**（版本 24~39，学 rea 的 il2cpp 模块）：从 `global-metadata.dat` 直接提取类型名、方法名与 StringLiteral 表。
6. **`.agentpack` 全家桶打包升级**：让 `internal/agentpack` 除了打包 `workflow`/`tool`/`innerapp` 外，还能一键打包 `skills`、`mcp_servers` 与 `.agentgo/*_adapters.json`，实现跨机一键复刻 Agent 进化成果。

## 7. 经验

- **真实文件比单元测试更能抓 bug**：HAR 的 BOM、ilspycmd 的 .NET 运行时版本，都是跑真文件才暴露的。
- **账本里的 gate 不能凭印象写**：本次先写了一条"sha 一致"，随后才发现我只比对了已解密的副本，于是重新从 APK 取原始条目、解码、对账后用 `supersedes` 覆盖——这正是 `supersedes` 存在的原因。
- **接口窄、答案小**：11 个工具、分页、落盘，比 109 个工具的菜单更省上下文，也更不容易让模型选错。
