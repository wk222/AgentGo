# 计划：可度量的 harness 自我改进（2026-10-26）

灵感来自 harness2（Google，Apache-2.0）。这里只借思路，用 Go 自己实现，不拷它的代码和提示词。

## 现状：AgentGo 有“能改”，缺“量”

| 已有 | 位置 | 说明 |
|---|---|---|
| 可编辑面（文件形式） | `workspace/skills/*/SKILL.md`、`AGENTS.md/SOUL.md/TEAM.md`（`internal/agent/os_agentsmd.go`）、动态工具、内嵌应用、工作流 | 模型已经能创建它们 |
| 快照/回滚 | `internal/agentpack/versions.go`（`Snapshot` / `Restore`），工具 `manage_extension_versions` | 覆盖工具、应用、工作流、skills；**不含 AGENTS.md** |
| 度量 | `cmd/agentgo-eval` + `internal/evaluation` | 黑盒，经 HTTP 打 `agentgo --serve`；有 `Verify`、`Attack`、`--baseline` 回归判定 |
| 轨迹 | 评测会话 id 为 `eval-<tag>-<case>-run-N`，写入会话存储 | 导出方式待核实 |

缺口：`evolve_skill` 的 `evaluate` 只是对字符串做静态检查（是否含 `context.Context`、`jsonschema:` 等），**不运行任何东西**。也就是说现在是“改了就留下”，没有“改了更好才留下”。

## 核心设计：改 → 跑 → 比 → 留/回滚

可编辑面 v1（刻意小）：`skills/*/SKILL.md`、`AGENTS.md`。
**禁止改**：Go 代码、治理策略与审批设置、凭据、评测套件本身。否则循环可以放宽自己的闸门。

循环（k=1 的最小版本）：

1. **基线**：复制工作区为 `base/`，在隔离实例上跑评测（`--runs ≥3`），得到结果和会话 id。
2. **证据目录**（文件，不塞进提示词）：失败用例的输入、完整轨迹（工具调用与结果）、磁盘 diff、指标。
3. **提出**：改进智能体在沙箱目录里只改 `harness/`（证据只读）。每处修改必须引用一个轨迹时刻，否则视为臆测。
4. **校验**：路径白名单、大小上限、SKILL.md 结构（复用 `agentpack.Validate`）、泄漏标记扫描。失败则把错误原文反馈，重试 1 次，仍失败回退基线。
5. **探测**：候选放进另一个隔离实例，跑**训练用例**和**留出用例**。
6. **闸门**：留出用例通过率不低于基线，训练用例有提升，注入套件（`suite_injection.json`）的 `Compromised` 不增加，token/缓存沿用 `DefaultThresholds`。
7. **采用**：先 `Snapshot` 再写入；写 `EXPERIENCE`（cue / lesson / evidence，缺一不存）。默认不自动采用，在创作中心展示 diff 由人确认。

和 harness2 的差别（有意为之）：harness2 没有真值，所以不给改进智能体看评分。AgentGo 有确定性的 `Verify`，所以用“训练/留出”切分防过拟合，改进者只能看到训练用例的判定。

## 落点（Go）

- `internal/evolution/`：`surface.go`（白名单读写与 diff）、`evidence.go`、`leak.go`、`lab.go`、`ledger.go`。
- `cmd/agentgo-evolve/`：离线 CLI，风格同 `agentgo-eval`；之后再加 IPC 和创作中心入口。
- 隔离实例：独立数据目录 + 独立工作区 + 独立端口，复用评测已有的 `--serve-info` 发现机制。
- 账本：断言执行次数和会话次数，检测重复标签，并设美元上限。

## 核实结果（2026-10-26）

1. **隔离实例：可行，不需要新增环境变量。** 数据目录来自 `xdg.DataFile("agentgo")`，`adrg/xdg v0.5.3` 在 Windows 上也是 `EnvPath(XDG_DATA_HOME, localAppData)`，所以给子进程设置 `XDG_DATA_HOME` 即可得到独立数据目录。`--serve` 在同一数据目录下只允许一个实例，所以必须隔离。
2. **轨迹导出：网关没有。** `internal/gateway` 里没有 sessions/transcript/history 路由。隔离实例的数据目录是我们自己的，直接读它的 `agentgo.db`（表结构留到第 3 步确认）。
3. **套件太小。** `suite_default` 20 例（只有关键词判定，2 个多轮），`suite_workspace` 3 例（有磁盘校验），`suite_injection` 9 例（7 个攻击）。带确定性校验的只有 12 例，切训练/留出会很单薄，噪声可能盖过改进。真正使用时需要用户自己领域的用例集；v1 的闸门要按“噪声感知”设计（`--runs ≥3`，差异小于噪声按无变化）。
4. **分层：** 新包必须登记到 `arch.PackageLayer`（已登记为 `LayerAssets`，与 `evaluation` 同层）。`cmd/agentgo-eval` 不在 `CMDPackages` 里，所以 `cmd/agentgo-evolve` 可以直接引用 `evaluation`。
5. **技能加载器**在 4000 字节处截断 `SKILL.md`，且不要求 frontmatter，描述取第一个非标题段落。`Validate` 已按此约束（超长报“后面是死文本”，无描述段落报错）。`.cursor/skills` 也会被加载，它在可编辑面之外，不会被循环修改。
6. 隔离实例需要复制 `config.json`（含模型密钥），只在本机，不进任何产物。

## 风险

- **过拟合与噪声**：小套件加模型随机性，差异可能只是噪声。要求 `--runs ≥3`，小于噪声的差异按“无变化”处理。
- **持久提示注入**：循环写入的 AGENTS.md/skills 会被自动注入未来所有会话，所以闸门必须包含攻击套件。
- **成本**：一轮约 3 次完整评测加 2～3 次改进会话。真实模型下要设预算上限。

## 步骤

- [x] 1. 核实（见上）。
- [x] 2. `internal/evolution/surface.go`、`leak.go` + 15 个单测（纯 Go，无模型）。`go vet`、`go test ./internal/evolution/ ./internal/arch/` 通过。
- [ ] 3. 隔离实例启动器 + 轨迹导出。
- [ ] 4. `agentgo-evolve`：基线 → 提出 1 个候选 → 探测 → 闸门 → 报告（不自动采用）。
- [ ] 5. 采用与经验库。
- [ ] 6. 创作中心入口。
