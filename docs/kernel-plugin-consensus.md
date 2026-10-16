# AgentGo「全能终端」内核与插件共识方案 v1

> 状态:定稿(2026-10-10)。由项目作者与 Muse(muse.ai 架构顾问)三轮讨论形成,
> 并已对照真实代码核对。原始往返记录:`D:\tools\muse-cli\prompts\agentgo_plugin_a{1,2,3}.md`。
> 与 `plugin-architecture-plan.md` 的关系:本文是它的收敛与修订;P0/P1 已完成部分不重做,
> 其中「5.3.3 没有跨轮历史」已过时,以代码为准。

## 1. 设计原则

1. **协议先行、只增不改**:先冻结 `engine.Event` 的扩展,再做进程外插件。
2. **内核掌生命周期,插件掌能力**:Turn、取消、审计、权限的执行点在内核;模型、工具、渠道、解析器都是插件。
3. **信任定边界**:一方低延迟走进程内;第三方、重依赖、易崩走进程外。不按功能分进程。
4. **单一真相源**:工具的可见性、风险、grant 从 Capability Bus 一处派生。
5. **先自举、后生态**:先用一方切片验证契约,再开放第三方,不为假想插件设计槽位。

## 2. 内核边界

### 已存在(保留,不重做)

- 插件宿主:`internal/bridge` 装配 16 个 Protected 核心插件(storage、model、memory、sessions、checkpoints、
  approvals、capability、workspace、tools、agent、workflow、apps、toolset、kanban、taskhub、scheduler)、
  5 个可选插件(gateway、distill、capability-sync、ide、triggers),以及 codetools、crushproto、hostlink。
  守卫:`internal/bridge/runtime_boot_test.go`。
- Model 统一:`model` 插件提供 `ModelSource`,agent 经 `llmSettingsFrom(ModelSource)` 取配置。
- 跨轮历史:`agent.WithHistory` + `sessionHistory` 已接入 `app_ipc.go`、`stream.go`、`engine_ipc.go`、`crushproto_runner.go`。
- 事件骨架:`engine.Event{RunID, SessionID, Engine, Seq, Type, Payload, At}`。

### 待新增(最小增量)

- `engine.ContentBlock` 与 Event 扩展(第 4 节),落在 `internal/engine`(L1)。
- `internal/audio`(L1,新包):只含帧、事件与取消接口,不含 VAD/ASR/TTS 实现。
- `internal/bridge` **冻结**:只许装配接线,新类型与新逻辑落到对应包,arch 测试拦截。

### 明确不入内核

具体记忆召回排序、cron 细节、LLM 厂商适配、Inner App 渲染、渠道协议、文档解析实现、前端组件。

### 关于 Protected(决策 B)

保留布尔值。16 个核心插件走同一契约,本身就是「核心服务」。新增一条守卫:
Protected 集合是显式白名单,新增成员必须同时改 `runtime_boot_test.go` 并写决策记录。

## 3. 待新增的插件接口

引擎、工具(`Contribute("tool")`)、Workflow 不新造接口。待新增:

```go
// 渠道适配器:微信/飞书/麦克风等 I/O。internal/channels 目前只有配置。
type AdapterPlugin interface {
    Ingest(ctx context.Context) (<-chan Message, error) // 外部 -> 内核
    Emit(ctx context.Context, msg Message) error        // 内核 -> 外部
    Caps() AdapterCaps // CanStreamAudio, CanInterrupt, SupportsCard
}

// 模态插件:ASR/TTS/图生,不入内核
type ASRPlugin interface {
    Transcribe(ctx context.Context, audio <-chan AudioFrame) (<-chan Transcript, error)
}
type TTSPlugin interface {
    Synthesize(ctx context.Context, text <-chan string) (<-chan AudioFrame, error)
}

// 解析/索引插件:PDF/表格/OCR,默认进程外
type IndexerPlugin interface {
    Parse(ctx context.Context, ref MediaRef) ([]ContentBlock, error)
}

// 内核侧音频会话(internal/audio)
type AudioSession interface {
    Start(ctx context.Context, sessionID string) (<-chan AudioEvent, AudioControl, error)
}
type AudioControl interface {
    Interrupt(reason string) error // 发 interrupted 并取消当前 Turn 的 ctx
    Close() error
}
```

技能包与 agentpack 是分发单元,不是运行时接口。

**可打断时序**:麦克风帧 → ASR 插件出 partial → 内核在 TTS 播放中检测到新 speech 起点判定 barge-in →
`AudioControl.Interrupt()`:取消当前 Turn 的 ctx、发 `interrupted` → TTS 因 ctx 取消停播、渠道停渲染 →
ASR 定稿文本作为新 Turn 的入口。

## 4. 协议 v1(在 `engine.Event` 上增量扩展)

不新造并行的 StreamEvent。

| 位置 | 新增 | 说明 |
|---|---|---|
| `Event.Type` | `audio_chunk`、`card`、`interrupted`、`input_transcript` | `interrupted` **不是终局** |
| `Event.Payload` | `block`: `ContentBlock` | 存在时为权威内容,优先于旧文本字段 |
| `ContentBlock.Type` | `text` `image` `audio` `file` `card` `tool_call` `tool_result` `thinking` | `Media` 只放引用(路径/hash/尺寸),不塞大块 base64 |
| `RunRequest` | `Blocks []ContentBlock` | 与旧 `Input`、`Images` 并存;内核归一化为 Blocks |

### 兼容规则(不可破坏)

1. 只增字段与 Type 值,不改名不删除;`RunID` 即 Turn 标识,不另起 `TurnID`。
2. 消费者必须忽略未知 Type 与 Payload 键;生产者不得依赖对方已识别新类型。
3. 同一 Run 内 `Seq` 单调递增;每个 Run **恰好一个** `done`,由 Router 发,引擎不得自发 `EventDone`。
4. **打断语义**(决策 G):`interrupted` 是 `done` 之前的普通事件;被打断的 Run 仍以唯一的 `done` 结束,
   `Payload.reason = "interrupted"`。`interrupted` 之后 Router 丢弃该 Run 后续的 `token/audio_chunk`,
   只允许已在途的 `tool_result/error` 落地。
5. 空 `tool_result` 由内核归一化为结构化空结果再入历史,引擎不得各自处理。
6. `Images []string` 至少保留一个大版本,新代码只写 `Blocks`。

## 5. 进程外插件

Manifest(P2 前冻结):

```json
{
  "id": "dev.agentgo.pdf-parser",
  "version": "0.1.0",
  "apiVersion": ">=1.0 <2.0",
  "type": "indexer",
  "entry": { "command": "agentgo-plugin-pdf.exe", "args": ["--stdio"] },
  "provides": ["indexer.pdf"],
  "requires": [],
  "permissions": { "network": [], "filesystem": ["${PLUGIN_DATA}"], "capabilities": ["file.read"] },
  "resources": { "memoryMB": 512, "restartBudget": 3 }
}
```

- **协商**:`initialize`(插件报 apiVersion 与能力)→ 内核下发「声明权限 ∩ 用户批准」的实际 grant → `ready` → 业务调用。
  版本不合或必需服务缺失,置 Skipped 并给可读原因。
- **权限**:声明、批准、执行三段一致;插件间调用一律经 Capability Bus 并带调用方身份,
  权限取调用方 grant 与被调方要求的交集。第三方默认无网络、无任意文件。
- **监督**:stdio JSON-RPC + 流式通知;Windows Job Object 收口;内存上限硬杀;崩溃指数退避,
  5 分钟内超 `restartBudget` 熔断并在 UI 标红。MCP 只作为工具类适配接入 `pkg/mcphost`,不承担渠道与语音双向流。

## 6. 垂直切片:迁移 PDF 解析为进程外插件(决策 F、H)

代码里已有进程内的 `internal/tools/docparser.go`(eino-ext 的 pdf/docx/xlsx 解析器)。
本切片是**迁移而非新增**,一次验证:file/card 协议、进程外 sidecar、文件权限、前端槽位。

流程:桌面端拖入 PDF(`file` 块)→ 内核按 grant 只授该文件读权 → 进程外解析插件产出文本块与页码引用 →
agent 以 `card` 块返回引用列表 → 前端 `chat.card` 渲染 → 卸载插件后槽位零残留。

验收:
1. 同会话追问能引用上轮解析结果(走已有 History)。
2. 杀掉解析进程,整机不卡且熔断可见。
3. 日志与审计可按 RunID 串起全链。
4. 无网络权限下插件联网被拒并留痕。
5. `docparser.go` 变成经 Capability Bus 调插件的薄壳;内核不再直接 import eino-ext 的 pdf/docx/xlsx 解析库(import 守卫校验)。
6. 迁移前后同一批样例文件的解析结果与页码引用一致。

## 7. 路线(关键路径串行约 7–8 周)

| 阶段 | 内容 | 验收 | 预估 | 坑 |
|---|---|---|---|---|
| 0 | Crush sidecar 的 provider/model 目前来自外部 `crush.json`(`AGENTGO_CRUSH_CONFIG_DIR` → `CRUSH_GLOBAL_CONFIG`),与 `model` 服务是两个来源(代码里没有同步逻辑,只有这个旁路);改为由 `ModelSource` 生成 crush 配置。工具可见性四处判断(注册表、`RegisterStaticTool`、`StaticToolAllowlist`、governance 风险表)做审计,产出结论 | crush 引擎模型配置只剩 `model` 服务一个来源;审计报告 | 约 1 周 | 别顺手重构记忆 |
| 1 | 协议 v1:`ContentBlock`、4 个新 Type、空结果归一化、`interrupted` 语义;一个进程内 echo 工具 + echo 适配器 | 四条已有路径跑同一多轮用例行为一致;旧消费者忽略未知 Type | 1–2 周 | 「只增不改」从这步立起来 |
| 2 | P2 窄版:仅 `tool/indexer` 两类 stdio sidecar;manifest、协商、Job Object、重启预算 | 故意 panic/死循环/爆内存,整机可用且熔断可见 | 2–3 周 | 别一上来支持全部类型 |
| 3 | 垂直切片(第 6 节)+ 前端 `chat.card`、`settings.page` 两个槽位 | 第 6 节验收 | 约 2 周 | 槽位为真插件设计 |
| 4 | 仅保留:「核心不得 import 插件」arch 守卫、Protected 白名单守卫、版本锁最小实现 | arch 测试变红能拦反向 import | 约 0.5 周 | — |

并行:阶段 0 的 crush.json 清理可与阶段 1 并行;阶段 3 的前端槽位在协议冻结后可与阶段 2 后半并行。
若只有 3 个月:收敛为「协议冻结 + 一个真进程外解析插件 + 前端卡片闭环」。

## 8. 决策记录

| 点 | 分歧 | 结论与理由 |
|---|---|---|
| A 协议 | 新造 StreamEvent vs 扩展 | 扩展 `engine.Event`。四条路径已在用,并行协议等于双倍维护 |
| B Protected | 废除分两层 vs 保留 | 保留并加白名单守卫;16 个核心插件同契约且已有测试 |
| C 服务版本 | 带版本 vs 推迟 | 推迟到 P2,用 manifest `apiVersion`;服务接口不兼容变更时换新名并加接口快照测试,禁止原地改语义 |
| D 第 0 步 | 三个洞 vs 残留 | History 与 Model 统一已完成,不列任务;只剩 crush.json 同步与可见性审计 |
| E 语音打断 | 内核范围 | 内核只管 `AudioSession` 与取消令牌;VAD/ASR/TTS 为插件;语音整体排在切片之后 |
| F 首个切片 | 全平台 vs 单场景 | 单场景 PDF 迁移切片 |
| G 终局事件 | `interrupted` 终局 vs 普通事件 | 普通事件;保持「每 Run 恰好一个 done」(done 带 `reason=interrupted`) |
| H 切片性质 | 新增解析 vs 迁移 | 迁移,并以 import 守卫证明内核不再依赖解析库 |
| I 新类型落点 | bridge vs 新包 | `ContentBlock` → `internal/engine`(L1);`AudioSession` → `internal/audio`(L1);bridge 冻结 |
| J Notification Router | 现在做 vs 推迟 | 推迟:没有渠道实现就没有消费者,等第一个渠道适配器落地再补 |

## 9. 实施进度(随开发更新)

### 阶段 1 · 协议 v1 内核部分 —— 已完成(2026-10-10)

| 项 | 位置 | 说明 |
|---|---|---|
| `ContentBlock` / `MediaRef` / `NormalizeRequest` / `ValidateBlocks` | `internal/engine/content.go` | 8 种块类型;禁止 `data:` 内联媒体;文本/Data 上限 256 KiB;未知块类型放行 |
| 新事件类型 | `internal/engine/types.go` | `audio_chunk` `card` `interrupted` `input_transcript` |
| `RunRequest.Blocks`、`RunResult.Reason` | 同上 | Router 双向归一化 Blocks ↔ Input/Images,旧引擎无需改 |
| 向后兼容的发射接口 | `engine.BlockEmitter` + `EmitBlock/EmitInputTranscript` | **没有给 `Emitter` 加方法**:`crushengine` 的测试替身实现了它,加方法会破坏兼容;新接口是可选的 |
| 流规则 | `internal/engine/emitter.go` | `Seq` 单调;每个 Run 只有一个 `done`,`done` 之后任何事件被丢弃;`interrupted` 之后丢弃 `token/audio_chunk`,放行在途 `tool_result/error`;sink 内可安全调用 `Router.Interrupt`(锁不跨 sink 调用) |
| 空工具结果归一化 | `emitter.ToolResult` | 空输出 → `(no output)` + `payload.empty=true`,所有引擎统一,与 `codetools` 现有字面量一致 |
| 打断 | `Router.Interrupt(runID)`、`Router.InterruptSession(sessionID)` | 结果 `Reason="interrupted"` 且 `Error=""`;`done.payload.reason="interrupted"` |
| `internal/audio`(L1) | `internal/audio/audio.go` | 帧、事件、`Control`、`NewControl`;不含 VAD/ASR/TTS;`arch` 已登记 |
| Protected 白名单守卫 | `internal/bridge/runtime_boot_test.go` | `TestProtectedPluginsAreAnExplicitAllowlist` |

测试:`go test ./internal/engine/... ./internal/audio/... ./internal/arch/... -race` 与
`./internal/bridge ./internal/agent ./internal/crushproto` 全绿;新增 13 个协议用例
(`internal/engine/protocol_test.go`)。

### 阶段 0 · 工具可见性与风险 —— 已收敛(2026-10-11)

**更正(重要):** 此前这里写「确认存在静默不可见」是夸大了。实测(58 个注册工具)三张名字表在启动时是一致的,
没有「注册了但看不见」的工具。真正的问题是**结构性重复 + 总线与治理脱节**:

1. 可见性(`tools.staticToolNames` / `RegisterStaticTool` / `SessionMode.StaticToolAllowlist`)、
   风险(`governance.defaultRiskTable`)、可变性(`RegisterToolTraits`)分散在 3 个包的名字表里,一个插件工具要在多处登记。
2. Capability Bus 的 `risk_level` 从不由治理喂入(`syncAllCapabilities` 不带风险,`Bus.Register` 只按名字给 `execute_bash` 标 high),
   所以 UI 上展示的风险与实际审批策略可以不一致。

已做:

- `governance.ToolTraits` 增加 `Risk`;新增 `governance.StaticRisk(name)` 作为「这个工具多危险」的**唯一答案**
  (默认表、工具自声明、always-approve、文件变更 ≥ MEDIUM 取最大);`EffectiveRisk` 与审批判定也读自声明风险。
- 新增 `tools.Spec` / `tools.Declare`:一条声明同时写入「静态可见 + 是否改文件 + 风险 + 来源」,并一起撤销。
  codetools 插件改用它(在 `Contribute` 之前声明,总线 grant 创建时就能看到风险);
  `code_*` / `lsp_*` 的 14 条名字从 `defaultRiskTable` 移除,风险由插件声明(改文件 MEDIUM,只读 LOW)。
- `capability.Bus.SetRisk`;桥接层 `toolRiskLabel` 在 `Contributed` 钩子与 `syncAllCapabilities` 里写入,总线风险 = 策略风险。
- 前端能力面板把 `critical` 和 `high` 一样标红。
- 守卫测试(`internal/bridge/tool_catalog_guard_test.go` + codetools 插件测试)现在把以下约定变成红灯:
  每个注册工具至少在一个非 admin 模式可达;模式白名单不含未注册名字;风险表无死条目(外部工具包 `open_ipa_*` 等白名单豁免);
  总线风险 = 策略风险;改文件的工具 ≥ MEDIUM;`Declare` 一条记录、一起撤销。已用变异验证(去掉 `SetRisk` 立即红灯)。

**没做、且是有意的:** 每个模式的白名单字面量(`mode_policy.go`)仍按名字写,它是产品策略,由守卫保护;
内置工具的风险表仍在 governance 内。这是「插件声明的工具 + 总线/治理一致」的部分收敛,不是把所有内置表重写。

### 阶段 0 · Crush 融合 —— 已完成:去掉 Crush 自己的实现(2026-10-11)

原先有三条 Crush 相关路径:

1. `codetools`:Crush 的工具(LSP/编辑/搜索)在进程内绑定到 AgentGo 的 Eino 引擎;
2. `crushproto`:AgentGo 对外讲 Crush 协议,Crush 的 TUI 当客户端,由 Eino 驱动;
3. `crushengine` + `engine_crush.go`:把外部 `crush.exe` 当 sidecar 引擎启动,跑**它自己的 agent 循环和它自己的模型配置**(外部 `crush.json`)。

第 3 条是唯一的第二套 LLM 栈,与前两条功能重叠,也是「模型配置有两个来源」的根源。**已删除**(约 2000 行):

- 删除 `internal/engine/crushengine/` 与 `internal/bridge/engine_crush.go`、其 e2e 测试;
  `AGENTGO_CRUSH_EXE` / `AGENTGO_CRUSH_CONFIG_DIR` / `AGENTGO_CRUSH_AUTOAPPROVE` 不再有意义
  (`AGENTGO_CRUSH_EXE` 仅剩在两个 e2e 测试里表示「作为 TUI 客户端的 crush.exe」)。
- 新增 `coding-agent` 插件(`bridge/coding_agent.go`):贡献 `coding_agent` 工作流节点,`crush` 作为节点类型别名保留,
  老工作流能继续加载;引擎名 `crush` 映射到 Eino。该节点在 Eino + codetools 上跑,无第二套模型配置,
  不再需要「由 ModelSource 生成 crush.json」——来源本来就只剩 `model` 服务一个。
- Eino 只上报工具调用、不上报文件变更,所以节点的 sink 在改文件的工具调用前后做快照 diff,
  填 `ChangedFiles`(与 TUI 适配器同一套 `snapshotBefore`/`scanTree`)。
- 能力探测里 `chat.crush` 换成 `coding.agent`。

**行为差异(有意接受):**

- Eino 的编辑工具绑定在**当前活动工作区**;节点的 `work_dir` 与活动工作区不一致时现在**报错**,而不是静默改错目录。
- 节点里遇到需要审批的工具会返回「等待审批,工作流还不能批准」,不再有 Crush 的 workspace-scoped yolo 自动批准;
  工作流内审批是后续要补的能力。
- 不再能选「Crush 自己的循环」作为引擎;要用 Crush 的界面,走 `crushproto`(TUI 当客户端)。

### 工作流内审批 —— 已完成(2026-10-11)

`coding_agent` 节点遇到需要审批的工具时,不再报错,而是**暂停工作流等人批**,批完继续:

- 节点把宿主的 `*workflow.CodingPause` 变成 `compose.StatefulInterrupt`,信息里带 `approval_id`,
  所以走现成的 `tool_approval` 路径(同一个审批弹窗、同一个 `resolveApproval`)。
- 批准后工作流恢复,节点用保存的状态继续**原来那次 agent 运行**(`resumeAgent`),不重放上游输入。
  暂停前已做的工具调用数与文件变更随状态带过去,最终结果覆盖整个节点。
- 拒绝则节点失败(「xxx was not approved」),被拒的工具不会被放行。
- 没开工作流检查点(`CanPause=false`)时仍给出清楚的错误,不会悬挂。
- **顺带修了一个已有缺口:** `resolveApproval` 的 workflow 分支把「恢复后又暂停」当成 `resume_error`,
  第二个审批根本没登记;现在会像第一次一样登记并发 `approval:pending`,审批可以连续串起来
  (对所有会暂停的工作流节点生效,不只 coding_agent)。
- 测试 `coding_agent_hitl_test.go` 走完整链路:暂停 → 批第一个 → 再暂停 → 批第二个 → 结果;另有拒绝、不能暂停两条。
  写测试时抓到一个真 bug:恢复时节点重算提示词得到空串被误报「empty task」,已修(恢复路径不要求提示词)。

### 阶段 2 · 文档生成(Go 原生,不走 sidecar)—— 第一个垂直切片已落地(2026-10-11)

决定用 Go 原生实现,所以这个切片**不需要进程外 sidecar**;sidecar(stdio JSON-RPC、Job Object、重启预算)
仍保留为「第三方/非 Go 插件」的方案,等真有这类需求再做。

- 新包 `internal/officedoc`(L2):
  - `docx.go`/`docx_parts.go`:自己写的 WordprocessingML 写入器,无第三方依赖。标题 1–6、段落内粗/斜/删除线/行内代码、
    多级项目符号与编号列表(每个编号列表各自从 1 开始)、表格(表头加粗着色、跨页重复)、代码块、引用、分隔线、分页、
    CJK 字体(微软雅黑)。XML 非法控制字符会被丢弃,否则 Word 会报文件损坏。
  - `markdown.go`:用 goldmark 把 Markdown 转成上述结构,模型直接写 Markdown 即可。
  - `xlsx.go`:基于 excelize。数字/布尔保持类型,`=` 开头的字符串写成公式(文件格式里不带 `=`),
    首行表头加粗着色并冻结、加筛选,按 CJK 宽度估算列宽,工作表名自动清洗并去重(≤31 字、无非法字符)。
  - `tools.go`:`create_docx`、`create_xlsx` 两个 Eino 工具;已存在的文件默认拒绝覆盖,临时文件写入后改名,失败不留半截文件。
- `officedoc` 插件(`bridge/officedoc_plugin.go`):`tools.Declare` 声明为静态可见 + 改文件 + MEDIUM 风险,
  只读画布里自动隐藏,走审批;路径每次调用时按**当前工作区**解析(`tools.ResolveWorkspaceFile` 做路径牢笼),切换工作区无需重载。
  `AGENTGO_OFFICEDOC=0` 可关闭。能力探测新增 `office.docs`。
- 验证:XML 良构、可被仓库自己的 DOCX/XLSX 读取器读回;并用独立的 python-docx 1.2.0 / openpyxl 打开核对
  (样式名、标题、列表、表格、公式、冻结窗格、筛选都对)。**没有在真正的 Word/WPS 里目视验证**,这一步需要人看一眼。
- **顺带修了读取侧的静默丢数据:** `parse_file` 读 .docx 用的是默认配置,**表格、页眉、页脚全被丢掉**;现已打开。

### 自有命令行 `agentgo-cli`(不依赖 Crush)

`cmd/agentgo-cli` + `internal/cli`。进程内跑 AgentGo 大脑(同一个引擎路由、治理、记忆、插件),不需要 crush 二进制,
也不走 Crush 协议。没有复用 Crush 的 TUI 代码——Crush 的 TUI 绑死在它的 `/v1` 协议和自己的会话模型上,
抽出来比自己写一个行式 CLI 更贵;需要全屏 TUI 时用已有的 `agentgo-tui`(嵌入 Crush TUI)。

- 分层:`internal/cli` 只依赖 `Backend` 接口(可无模型测试);`cmd/agentgo-cli/backend.go` 把它接到 `bridge.AppService`
  (硬规则:只有 `bridge` 包和 `cmd/*` 能 import `bridge`,所以粘合代码放在 cmd 里)。
- 命令:`agentgo-cli`(交互)、`run "任务"`(也可管道喂 stdin)、`sessions`、`plugins`、`tools`、`approvals`、`approve|deny ID…|all`。
  参数:`-w` 工作区、`-session`/`-continue`、`-yolo`、`-json`(每事件一行 JSON)、`-thinking`、`-api responses`、`-v`。
- 输出约定:stdout 只有答案(可管道),工具调用/结果/文件变更/推理走 stderr;`-json` 时 stdout 只有 JSON 行。
- 审批:交互时逐个问 `[y]es/[n]o/[a]lways`;`-yolo` 自动批准;**无人可问(非终端)时不替用户决定**,退出码 3 并打印待批项,
  之后可用 `approve|deny` 在另一个进程里决定,批准后会继续跑完(已实测跨进程恢复)。`ask_user` 的提问同理(交互时读一行作答)。
- 退出码:0 成功、1 出错、2 用法错、3 需要审批/作答、130 被 Ctrl+C 中断。
- 运行时日志默认只写 `agentgo.log`(`applog.SetQuiet`),`-v` 才回显到 stderr。
- 已实测(真实 Azure 模型、隔离的 `XDG_DATA_HOME`):`run` 生成 docx(python-docx 读回正确)、无 yolo 退出码 3、`approve all` 跨进程续跑、
  管道喂入的多轮对话(上下文记忆正确)、`-continue`、`-json`。**没有在真实终端里手测**逐项审批提示和 Ctrl+C(只有单元测试覆盖),需要人试一下。
- 已知不足:
  1. 恢复(批准后)那一段没有逐 token 流式输出,只在结束时一次性给文本,也看不到恢复后工具的 ✓ 行(恢复路径的事件只发给桌面端)。
  2. 每次启动要拉一次 Catwalk 供应商表(codetools 启动),多 2–3 秒,且离线时会拖慢;应改成惰性/缓存。
  3. `bytedance/sonic` 在 go1.27 下每次启动往 stderr 打一行告警,不是 CLI 的锅,但很碍眼。
  4. 提问的选项列表(`choices`)到不了 CLI,目前只能自由文本作答。
  5. 只做了进程内模式;和桌面端同时运行时共用同一份数据目录(SQLite),尚未做“连到已运行的宿主”。
  6. Markdown 不渲染(原样输出)。

### 下一步(按优先级)

0. `agentgo-cli`:恢复路径的事件也要进 CLI 的 sink(`resolveApproval` 现在用桌面 sink);Catwalk 拉取改惰性;再考虑连宿主模式。
1. 人工在 Word / WPS 打开 `create_docx` 的样例,确认版式(字号、间距、表格宽度)满意;不满意只需改 `docx_parts.go` 的样式表。
2. 文档**编辑**能力:读取现有 .docx 后做局部替换/追加(目前只能新建)。需要决定是否保留原有样式(模板填充)。
3. 第二个垂直切片:企业微信/微信渠道适配器(这时才真正需要通知路由器与进程外插件)。
4. 前端 `chat.card` / `settings.page` 插槽;阶段 4 的架构守卫(内核不得 import 插件、版本锁)。
5. 仍未决定:`frontend-opensumi/`、`frontend-legacy/` 是否删除,`eino-ext/` 是否从 git 移除。

### 明确推迟

服务名版本化(至 P2)、Notification Router(至首个渠道落地)、完整实时语音与跨设备同步(至切片验收后)、
agentpack 签名与市场、scheduler 迁移、记忆写路径插件化、前端任意 WebComponent 沙箱(先只做声明式卡片)。

### 需要核实

- (已结论)工具可见性当前无静默不可见,见上节更正。
