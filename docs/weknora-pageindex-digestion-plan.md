# 消化 WeKnora 与 PageIndex：三个原生插件（方案）

日期：2026-10-28
状态：D0（分块器）与 D4a（VLM OCR）已落地；D1–D3、D4b、D5 待做。构建策略已于 2026-10-28 改为「一条分支、按能力组装」，见 §0.1。

2026-10-28 后续：D1 的来源范围、PDF 页码、XLSX 全表行号、版本校验续读及结构化父子分块预览已实现；snippet/normalize/chunkmerge 尚未完成，不能把 D1 标为整体完成。另已接入原生 web_fetch、执行级证据表与回答引用原文快照入口。新增实施记录见 [weknora-implementation-2026-10-28.md](weknora-implementation-2026-10-28.md)，包含浏览器/搜索吸收顺序与未验收项。

## 0. 结论

不接 WeKnora 的 HTTP API，也不 vendor 它的服务层。**只搬算法包，落成 AgentGo 的原生插件**。

理由：WeKnora 的 Go 代码里，真正有复用价值的是「文档切分、检索结果后处理、OCR 输出校验、原文定位」这一层纯计算逻辑，
它们几乎不依赖框架；而它的服务层（租户、RBAC、共享空间、IM 渠道、Wiki、Graph、向量库适配、Wails 桌面壳、Python
docreader）要么与 AgentGo 已有能力重复，要么和「单 exe + Win7 + 内网」直接冲突。

PageIndex 只借一个范式：**用树索引 + 模型推理做检索，不用向量相似度**。AgentGo 已有
`internal/memory/summary_tree.go`（SummaryForest：层次摘要 + 证据溯源），缺的是「按文档建树 + 页码可回溯」。
WeKnora 的 `sourceloc` 与它的 `ContextHeader` 面包屑正好补这一块。

`laowang_go` 的 OCR 引擎（`internal/ocr` + `ortutil`）可移植，但**它依赖的 `onnxruntime.dll` 在 Win7 上无法加载**
（静态导入 `CreateFile2`，Win8+）。因此 OCR 改为主推 VLM 远程 provider，本地引擎只作 `-tags onnx` 备选，见 §4。

## 0.1 构建与组装：一条分支、一份源码、按能力组装

### 决定（2026-10-28，推翻了本文早先的方案）

早先的方案是给 Win7 单开 `win7` / `nokb` 两个构建标签，让知识库在编译期就不存在。**已收回。**
现在只有一条规则：**构建标签只用于「代码在某个目标上编不过、加载不了」；其余一律用运行时开关；平台差异不开分支。**

收回的依据，都是在这台机器上实测的：

| 事实 | 数据 |
|---|---|
| `win7` 分支与 `master` 没有任何分歧 | `git rev-list --left-right --count win7...master` 为 `0 0`；Win7 的全部工作（`cmd/agentgo-web`、`internal/webshell`、发布脚本、两份 Win7 文档）都是未跟踪文件，没有提交 |
| 知识库在 Win7 工具链下能编、能跑 | 旧工具链（go1.27.2 legacy）、`CGO_ENABLED=0`、`GOTOOLCHAIN=local`、`-tags web`：`go build ./...` 通过；按发布脚本的配方构建 `agentgo-web.exe` 后真实启动，`/api/ipc/ping` 通 |
| 编进去的体积很小 | `agentgo-web.exe` 66,104,320 B（含知识库）对 65,994,752 B（不含）：差 109,568 B，**107 KiB，0.17%** |
| 编进去的启动代价很小，但不是 0 | `GODEBUG=inittrace=1`：`docchunk` 包初始化 **1.0 ms、分配 1.2 MB（8,137 次）**，来自 26 处包级 `regexp.MustCompile`；`visionocr` 为 54 KB / 393 次 |
| 真正的硬约束只有本地 ONNX | `onnxruntime.dll` 静态导入 `CreateFile2`、`GetSystemTimePreciseAsFileTime`（Win8+），且 `yalue/onnxruntime_go` 需要 CGO |

`nokb` / `win7` 的错在于把两件不同的事绑在一起：「平台**做不到**」（ONNX）与「产品**不想要**」（知识库）。
为后者付出的是一个多出来的构建组合，以及我自己在落地时踩到的三个标签坑（见下与 §6.1），换来的是省 107 KiB。
这是方案的错，不是实现的错。

### 三种手段各管什么

| 手段 | 用在 | 例子 |
|---|---|---|
| **构建标签** | 代码在某个目标上**编不过、加载不了**，或依赖大到不该人人背 | `web`（不带 Wails/WebView2）、`milvus`、将来的 `onnx`（CGO + Win8+ DLL） |
| **运行时开关**（`internal/config/flags.go`） | 能跑，但不是每个人都想要 | `AGENTGO_DOCCHUNK`、`AGENTGO_DOCTREE`、`AGENTGO_OCR`，默认都是 0 |
| **分支** | 不用于平台差异；`win7` 只是个名字，分支只留给短期功能开发 | — |

「组装」指的是发布脚本：**工具链 + CGO 开关 + 标签 + 附带文件**。同一份源码，配方不同：

| 配方 | 工具链 | CGO | 标签 | 脚本 |
|---|---|---|---|---|
| Windows 7 网页版 | go-legacy-win7 | 关 | `web` | `scripts/release-win7.ps1` |
| 桌面版（Wails） | 本文不改它的打包流程 | — | 不带 `web` | 现有流程 |
| Win10+ 离线 OCR 版（将来，D4b） | 待定 | 开 | `onnx`（可与 `web` 同用） | 待写 |

以后 Win10+ 想加更多功能，先问一句「Win7 工具链下编得过吗」：编得过，就做成默认关的开关；
编不过（需要 CGO、新系统 API），才新增一个**能力标签**，并且只在对应配方里打开。**不需要为此开分支。**

### Win7 上的能力基线：等于今天，而不是退化

| 能力 | Win7 网页版 | 说明 |
|---|---|---|
| 记忆切分 | 内置 1200/200，即**今天的行为** | 移植的分块器编进去了，但 `AGENTGO_DOCCHUNK` 默认 0，不生效 |
| 文档树检索 | 不启用 | 同样是开关，默认 0（D2 落地后） |
| OCR | VLM 远程可用 | `AGENTGO_OCR` 默认 0；本地 ONNX 不可用 |
| ConPTY 终端 | 无 | Win10 1809+ 才有，本来就用不了（`docs/win7-branch-plan.md`） |
| 办公文档、治理、记忆、工作流 | 一样有 | 不受影响 |

「Win7 不要知识库」这条要求，现在靠**默认关**来满足，不靠编译期排除：Win7 的行为与今天一致，
`docs/eval-baseline.md` 的基线继续成立；代价是产物里多了 107 KiB 不会被执行到的代码。
将来如果某个发行物要求**一行都不能有**，给那个配方加一个标签即可，成本是每个包一行 `//go:build`，可逆。

### 保留下来的纪律

1. **知识库工具一律 `Static: false`**（经 `tool_search` 发现，走 `AllowsDynamicTool`），不进 `mode_policy.go` 的静态白名单。
   `TestModeAllowlistsNameOnlyRegisteredTools` 与 `TestEveryRegisteredToolIsReachableInSomeMode` 因此同时为绿，与标签无关。
2. **`memory` 不能 import `docchunk`。** 以前这条是被标签「顺便」保证的；现在写成明文规则：
   `internal/arch/plugin_rules_test.go` 的 `concretePlugins` 列表含 `docchunk` 与 `visionocr`，只有 `bridge` 能引用它们。
   已做反向验证：让 `memory` 临时 import 这两个包，`TestConcretePluginsAreOnlyReachedThroughTheAssembly` 如期失败并点名。
3. **新包照常注册 `internal/arch/layers.go`**：`docchunk` 在 `LayerSystems`，`visionocr` 在 `LayerAssets`。

### 给将来 `onnx` 标签留的坑（D0 阶段实测过）

1. **`go list ./...` 会把被排除的包也列出来**，只要目录里还有一个文件不满足标签条件——
   所以一个包的**所有文件，包括 `_test.go`**，都要带同一个标签。
2. **`go test <被排除的包>` 会失败**（`build constraints exclude all Go files`），而 `go test ./...` 会跳过它。
   发布与回归脚本只用 `./...`，不要点名包。
3. **产物断言的标记必须双向验证**，见 §6.1「断言标记的教训」。

### Win7 兼容性：已验证与未验证

已验证：旧工具链 + CGO 关，能编、能启动（默认关时 28 个插件，无 `chunker` / `visionocr`；
设 `AGENTGO_DOCCHUNK=1 AGENTGO_OCR=1` 后两者挂上，日志正确）。

**没有验证：在真实 Win7 机器上运行。** 发布回执里一直是 `windows7_machine_acceptance: pending`，这仍是唯一可靠的验收。

我之前拿来佐证的两条**没有判别力**，在此更正：

- 「PE 头 OS/子系统版本 6.1、静态导入只有 `kernel32.dll`」：实测 go1.20.14、stock go1.26.2、legacy go1.27.2
  三个工具链编出的 hello-world **全都**是 `OS=6.1 Subsystem=6.1`、只导入 `kernel32.dll`，所以它证明不了什么。
- 「产物里没有 Win8+ 的 API 名」：旧工具链的运行时先找 `bcryptprimitives.ProcessPrng`，找不到就回退 `RtlGenRandom`
  （其 `src/runtime/os_windows.go` 注释：bcryptprimitives.dll ships with Windows 7 but exports ProcessPrng only from Windows 8），
  所以产物里有 `ProcessPrng` 字符串是预期内的。字符串扫描区分不了 stock 与 legacy 工具链，
  也证明不了「没有无保护的 Win8+ 调用」，因此发布脚本里不加这类检查。

## 1. 已核对的移植清单

源码位置：本机的 WeKnora 克隆。依赖一栏只列非标准库导入。

### 1.1 值得搬（按价值排序）

| # | 包 / 文件 | 体量 | 依赖 | 判断 |
|---|---|---|---|---|
| 1 | `internal/infrastructure/chunker/`（11 个非测试文件） | 95 KB；配套 14 个测试文件 115 KB | 仅 `logger`（2 处 `Debugf`）+ `docparser.UnwrapLinkedImages`（1 处） | **最高价值**。自适应分层切分：`StrategyAuto/Heading/Heuristic/Recursive/Legacy`、文档画像（`DocProfile`）、标题面包屑（`ContextHeader`）、受保护区间（代码块/表格不被切断）、父子两层切分（`SplitTextParentChild`，子块小窗口检索、父块大窗口喂模型）、`ApproximateTokenCount` 多语言、`ValidateChunks` 兜底校验。AgentGo 现在的切分器 `internal/memory/chunker.go` 只有 119 行，按 `\n\n` 段落拼 |
| 2 | `internal/searchutil/snippet.go` | 1.2 KB | 无 | 正则命中点前后各 60 rune 的上下文摘要，压成单行。AgentGo 没有统一的「检索命中预览」 |
| 3 | `internal/searchutil/normalize.go` | 2.3 KB | `sort` | 泛型关键字分数归一化，≥10 条时用 P5/P95 截断抗离群。AgentGo 的 `mergeHybridRRF` 只做 RRF，没有独立的分数归一 |
| 4 | `internal/searchutil/chunkmerge.go` | 8.5 KB | `types.Chunk` | `JoinChunkContent`：按后缀/前缀重叠合并相邻块，保守保留重复而不是丢内容。做「读回整节」时必须 |
| 5 | `internal/application/service/ocr_sanitizer.go` | 200 行 | `html-to-markdown/v2`、`docparser` | VLM/OCR 输出的**校验**：识别解码重复（`hasOCRRepetition`）、剔除「无文字内容」这类空回答、去 markdown 围栏、HTML→Markdown。这是防止把幻觉重复写进索引的关键闸门，与 `vision_advisor` 天然配对 |
| 6 | `internal/sourceloc/`（`align.go` `assign.go` `remap.go` `text.go`） | 24 KB | `types`；`office.go` 另需 excelize（AgentGo 已有 v2.11.0）与 `x/net/html` | 把切片文本对回原文的行/页/坐标。做页码级引用用它，或按第 4 节用更轻的偏移法 |
| 7 | `internal/infrastructure/docparser/csv_converter.go`、`json_converter.go` | 8 KB + 9 KB | 无（纯标准库） | CSV/JSON 转 Markdown 表格/围栏，`parse_file` 目前对这两类直接当纯文本返回 |

### 1.2 不搬（含理由）

| 对象 | 理由 |
|---|---|
| `docreader/`（Python 解析服务，含 `pdf_parser.py` 92 KB、MinerU、PaddleOCR-VL） | 需要 Python/GPU/联网模型，与「单 exe、内网、Win7」冲突。PDF 版面分析不是本轮目标 |
| `internal/types/evaluation.go` 的 `gojieba` | CGO 依赖，会把默认构建的 `CGO_ENABLED=0` 打穿。用 AgentGo 现有 `tokenize`/`cjkPieces`（`internal/memory/scorer.go`）替代 |
| `internal/application/service/*`（租户、RBAC、共享、自动化、Wiki、Graph、Sandbox） | 业务域无关，或 AgentGo 已有更深的实现（治理、审批、调度、记忆） |
| `internal/router/`、`frontend/`、`cmd/desktop` | 服务端/桌面壳，AgentGo 有自己的 |
| `mcp-server/`、`packages/dsh-weknora` | 前者是 Python 客户端，后者是 dsh 的 TS 插件；只借它的**四个工具语义**作为参考 |
| 向量库适配（Milvus/Qdrant/Weaviate/Doris） | AgentGo 已有 `-tags milvus` 的可选实现与 SQLite FTS5 默认路径 |
| `searchutil/imageinfo*.go`、`image_pipeline_*` | 依赖 VLM 与 WeKnora 的图片资产表，二期再评估 |

## 2. 三个插件，内核零改动

沿用 `docs/kernel-plugin-consensus.md` 与 `docs/plugin-architecture-plan.md` 的契约，不新增内核接口、不改 Protected 白名单。

```
插件名        Provides       Inject                  Optional         开关（默认）               构建标签
────────────────────────────────────────────────────────────────────────────────────────────────────────
chunker      chunker        —                       —                AGENTGO_DOCCHUNK（0）      无
doctree      doctree        db, model, tools        chunker          AGENTGO_DOCTREE（0）       无
visionocr    —              tools                   —                AGENTGO_OCR（0）           无（本地 ONNX 引擎将用 onnx）
```

三个都是 `optionalPlugin`（失败不拖垮启动），**默认都关**，`AGENTGO_PLUGINS_DISABLE` 也能关。
没有构建标签：三个包在每个构建里都存在，「在不在」由开关决定，见 §0.1。

`visionocr` 在 Win7 上可用（VLM / 远程 provider 不需要 CGO）；只有本地 ONNX 引擎受 `onnx` 标签控制，见 §4。

只有 `chunker` 提供的是可选服务：`memory` 通过 `SetChunker` 钩子取用，没装就回落到现有 `ChunkContent`。
`memory` 不 import `docchunk`，这条由 `internal/arch` 的 `concretePlugins` 规则强制（§0.1）；
旧实现 `internal/memory/chunker.go` 保留作回落，不删。

### 2.1 `chunker`（L1，新包 `internal/docchunk`）

- 移植 `chunker` 包，**去掉**两处外部依赖：`logger.Debugf` → `log.Printf`（或直接删），
  `docparser.UnwrapLinkedImages` → 该函数是纯字符串处理，连同它依赖的 `markdownCodeLiteralRanges`、
  `fencedCodeLiteralRanges`、`codeFence`、`isEscaped` 等约 7 个小函数一起搬（都在 `image_resolver.go` 的 990–1570 行区间，
  零外部依赖）。
- 只保留公开面：`Split` / `SplitText` / `SplitTextParentChild` / `SplitterConfig` / `Chunk` / `DocProfile` /
  `ApproximateTokenCount` / `ValidateChunks`；内部类型（`span`、`splitUnit`、`headingBoundary`）保持不可见。
- 测试一起搬（115 KB 是现成的回归预言机，与 `internal/codetools/testdata/crush_golden.json` 同一思路）。
  唯一要改的是 `parent_child_offsets_test.go`——它 import 了 `sourceloc` 和 `types`，改成只断言偏移不变量。
- 注册进 `internal/arch/layers.go`：`"docchunk": LayerSystems`。
- 接线：`internal/bridge/chunker_plugin.go`，`optionalPlugin("chunker", []string{"chunker"}, nil, nil, ...)`，
  无构建标签；由 `AGENTGO_DOCCHUNK`（默认 0）开关。
- 消费点：
  - `internal/memory/hybrid.go` 的 `ingestRecord`（现在固定 1200/200）：加一个可选钩子
    `SetChunker(func(string, int, int) []string)`，由 chunker 插件在启动时装上；取不到就走旧路径。
    **这样 `memory` 不 import `docchunk`**，由 `internal/arch` 的规则强制（§0.1）。
  - `internal/tools/docparser.go`（`parse_file` 之后的分块预览，同样走可选钩子）
  - 新的 doctree

### 2.2 `doctree`（新包 `internal/docindex`，不塞进 `memory`）

原方案想放进 `internal/memory` 复用表与树视图，但那样记忆与文档树就绑成了一个包，而记忆是 Win7 的核心能力——
两者必须能分开编译、分开测试。所以开一个新包 `internal/docindex`（`LayerAssets`），整包无构建标签，插件默认关（`AGENTGO_DOCTREE`）。

它仍复用 `memStore` 的同一个 `*sql.DB`（注入 `svcDB`），只是表前缀独立为 `doc_index_*`，不碰 `memory_tree_*`
（那套键是 `scope/record`，语义是「会话记忆」，混进来两边都难改）。树的**可视化**继续复用 `tree_view.go` 的渲染，
那部分不引 docindex，只吃同样的节点结构。

```sql
doc_index_docs(id, scope, path, hash, title, page_count, built_at, model, status)
doc_index_nodes(doc_id, node_id, parent_id, level, seq, title, breadcrumb,
                summary, page_from, page_to, char_start, char_end)
doc_index_chunks(doc_id, node_id, seq, content, char_start, char_end, page)
```

- `internal/docindex/build.go`：建树（解析 → 画像 → 分层切分 → 每节点 2 句摘要）
- `internal/docindex/recall.go`：检索
- 插件 `internal/bridge/doctree_plugin.go`：`optionalPlugin("doctree", []string{"doctree"}, []string{svcDB, svcModel, svcTools}, []string{"chunker"}, ...)`
  注意 `svcMemory` **不列进 Optional**——列了就会在 memory 未起时把 doctree 判成 Skipped，而它其实不依赖记忆
- 工具（`tools.Declare`，均 `Mutating: false`，`Static: false`）：

| 工具 | 风险 | 行为 |
|---|---|---|
| `index_document` | LOW | 对工作区内一份 PDF/DOCX/MD 建树，后台跑，进度复用 `taskhub`；已建过则按文件哈希跳过 |
| `search_document_tree` | LOW | 模型从根往下逐层选节点，返回**节点摘要 + 面包屑 + 页码区间**，再按需取正文 |
| `read_doc_node` | LOW | 取某节点整段正文（父块大窗口），相邻节点可合并（用 `JoinChunkContent`） |

## 3. PageIndex 范式怎么落（不引向量库）

PageIndex 的说法是「相似 ≠ 相关」：向量检索给的是语义相近的片段，而长专业文档要的是**推理**到正确章节。
它的两步是「建树索引 → 沿树推理检索」。落到 AgentGo：

1. **建树**：`parse_file` 拿到的正文 → `docchunk.Split`（自动选 heading/heuristic 层）→ 章节父子关系 →
   每节点一句摘要（用 `AGENTGO_REWRITE_MODEL`，未设则用主模型）。摘要进 SQLite，不引 Milvus。
2. **检索**：模型先看根节点的子节点清单（标题 + 摘要 + 页码），选中若干；逐层下钻；命中后取正文。
   每步都有明确的「看过哪些节点」记录，**引用天然可解释**——这正是 PageIndex 的卖点，而 AgentGo 的记忆树已经有同类可视化。
3. **降级**：树未建好/建树失败 → 回落现有 hybrid recall（FTS5 + 可选向量），行为不变。
4. **零新增基础设施**：不引向量库、不引 embedder（除非用户已配），SQLite 一张表就够。

### 3.1 页码可回溯（比 sourceloc 更轻的做法）

`parse_file` 现在把 eino 解析器的 `[]Document` 拼成一个字符串就丢了页边界（`internal/tools/docparser.go:82`，
`Pages: len(docs)` 说明是一页一项）。改动只有一处：拼接时记录每页的 rune 起始偏移 `pageOffsets []int`。

`docchunk.Chunk` 自带 `Start`/`End` 偏移（原包就有这个不变量），于是「块 → 页码」是一次二分查找，
不需要 `sourceloc` 的对齐逻辑。这样得到的引用是

```
《报告》第 12–13 页 › 三、风险敞口 › 3.2 汇率
```

`sourceloc` 因此降级为 P3，只在需要 PDF 版面坐标（框选高亮）时才考虑。

## 4. OCR：先纠正一个错误结论

### 4.1 事实核对（已用 dumpbin 实测，不是推测）

**`onnxruntime.dll` 1.28.0 在 Win7 上无法加载。** `dumpbin /imports` 的静态导入表：

```
KERNEL32.dll
    DC CreateFile2                      ← Windows 8+
    31B GetSystemTimePreciseAsFileTime  ← Windows 8+
    39B InitializeSRWLock               ← Vista+，Win7 有
```

两个 Win8+ 的 API 都在**静态导入表**里（`/dependents` 无 delay-load 段），加载器在 `LoadLibrary`
时就要解析它们，Win7 上解析失败 → DLL 加载失败。这是确定性失败，不是「可能有问题」。

由此推出一个容易误判的点：**同一台 Win7 机器上的 sidecar 也救不了**，因为 sidecar 加载的还是这个 DLL。
sidecar 只能解决「主 exe 不背 CGO」，解决不了「Win7 上没有可用的 ORT」。

其他事实：

- `laowang_go/internal/ocr`（`paddle.go` 13.8 KB、`bundle.go` 3.9 KB、`ocr_windows.go` 4.3 KB）本身没有
  `import "C"`，但依赖 `github.com/yalue/onnxruntime_go`，该库主文件第 16 行有 `import "C"` → **必须 CGO**。
  `laowang_go/build.ps1` 也确实设 `CGO_ENABLED=1`。
- 模型：`PP-OCRv6_small_det.onnx` 9.4 MB + `PP-OCRv6_small_rec.onnx` 21.2 MB + `rec_char_dict.txt` 93 KB ≈ 31 MB。
- AgentGo 发布链路：`scripts/release-win7.ps1` 固定 `CGO_ENABLED=0`。

### 4.1.1 顺带发现的既有缺口

`internal/tools/vision_advisor.go` 里有 `SetGlobalVisionAnalyzer` 插槽，但**全仓从未调用它**——
所以 `vision_advisor` 永远走 fallback，只返回「文件大小 / 格式 / 尺寸」加模板话术，没有任何真实图像理解。
同时 `.agentgo/llm_profiles.json` 的 `role: "vision"` 配置可读写，但除自身 CRUD 外无消费者
（`loadLLMProfiles` 只有定义处和 CRUD 内部两处引用）。

即：图片能进模型（`UserInputMultiContent` + `ImageBlock`），但「用专门的视觉模型看图」这条路是断的。
OCR 应当修好这条路，而不是另起一套。

### 4.2 设计：OCR 是可插拔 provider，不是单一实现

| 实现 | CGO | Win7 | 质量 | 依赖 |
|---|---|---|---|---|
| **VLM 远程**（网关上的 Qwen-VL / GLM-4V） | 否 | ✅ | 最好：版面、表格、图注一起给 | 网关有视觉模型 |
| 本地 PP-OCRv6 ONNX | 是 | ❌ | 好（纯文字行） | 31 MB 模型 + ORT |
| 远程 OCR 服务（Win10+ 机器 / sidecar） | 否（客户端） | ✅ | 取决于服务端 | 局域网内另一台机器 |

**已确认内网网关有视觉模型**，所以主路径就是 VLM 远程：不需要 31 MB 模型、不需要 CGO、
不需要 ORT，Win7 照跑；而且能同时给出版面结构，正好是 D2 文档树要的。
本地 ONNX 在 Win7 上本来就是死的，只作为「完全离线 + Win10+」的备选。

### 4.2.1 D4a 施工记录（已完成）

| 文件 | 内容 |
|---|---|
| `internal/visionocr/visionocr.go` | `Provider` 接口、注册表、`Pick`（按可用性挑，不按名字排序） |
| `internal/visionocr/sanitize.go` | 搬 WeKnora `ocr_sanitizer.go`；去掉 `html-to-markdown`，用标准库剥标签 |
| `internal/visionocr/vlm.go` | OpenAI 兼容 `/chat/completions` + 图片 data URL，纯 `net/http` |
| `internal/visionocr/tool.go` | `ocr_image` 工具（动态、LOW、非 mutating） |
| `internal/bridge/visionocr_plugin.go` | `optionalPlugin("visionocr")`，注入 `ModelSource`，装 provider |
| `internal/config/flags.go` | `AGENTGO_OCR`（默认 0）、`_PROVIDER`、`_VLM_MODEL`、`VISION_MODEL`、`_TIMEOUT_SEC` |
| `internal/arch/layers.go` | 注册 `"visionocr": LayerAssets` |
| `scripts/release-win7.ps1` | 负向断言（无 onnx）+ **正向断言**（必须有 `ocr_image`，防止被误排除） |

移植时改掉的三个原逻辑缺陷（都写成测试了）：

1. **表格被误判为解码循环**。40 行相同的表格行归一化后是真周期串，`hasOCRRepetition` 直接拒绝。
   修法：检测前先剔除 Markdown 表格行——表格重复是数据，不是 loop。宁可漏检表格内的循环，
   也不能拒绝真表格。
2. **HTML 表格转换顺序错**。原逻辑先 `looksLikeHTML` → 剥标签，`<table>` 结构就没了，
   后面的 `tablesToMarkdown` 再也看不到它。修法：表格转换提前到任何剥标签之前。
3. **短表格被判为空**。「剥标签后不足 10 字符」的早退把一张只有表头的真表格杀掉。
   修法：该判断移到表格转换之后。

验证：`internal/visionocr` 共 26 个测试，含用 `httptest` 假网关验证请求形状
（model 名、Bearer 头、图片是 `data:image/png;base64,` 内联）、401 时错误里不泄漏密钥。

**未验证**：真实网关的视觉模型对中文截图的识别质量。`AGENTGO_OCR` 默认 0，
需要你给一个可用的视觉模型名，实拍几张图跑一遍再决定默认值。


```
internal/visionocr/
  provider.go          // 无标签：Provider 接口 + 注册表 + 空实现
  vlm.go               // 无标签：OpenAI 兼容 vision 接口，纯 net/http，无 CGO
  remote.go            // 无标签：HTTP OCR 服务（sidecar 或局域网服务）
  onnx_windows.go      // //go:build onnx && windows && cgo：本地引擎（尚未实现，D4b）
  ort.go               // //go:build onnx：ORT 环境单例（搬 ortutil，InitializeEnvironment 只能调一次）
  bundle.go            // //go:build onnx：模型/DLL 路径解析
  sanitize.go          // 无标签：输出校验（见 §4.4）
```

```go
type Provider interface {
    Name() string
    Recognize(ctx context.Context, img MediaRef, opts Options) (*Result, error)
}
```

`Result` 带 `Text` 与 `Lines []Line`（文本 + 坐标 + 置信度），VLM 与本地 ONNX 返回同一形状，
上层 `ocr_image` 工具不必分叉。

**注意：OCR 不属于「知识库」。** 之前的方案把它算进知识库，是错的——Win7 上 VLM 远程可用。
只有 `onnx_*.go` 受 `onnx` 标签控制。

### 4.3 构建与分发

- `-tags onnx`：编入本地 ONNX 引擎。仅 Win10+ + CGO 环境。**尚未实现（D4b）。**
- 默认与 `-tags web`：只有 VLM / 远程 provider，**无 CGO、无 onnx 符号**。
- `cmd/agentgo-ocr`（`//go:build ocr`）：独立 `agentgo-ocr.exe`，以 MCP over stdio 暴露 `ocr_image`，
  供「局域网内一台 Win10+ 机器当 OCR 服务」的场景。AgentGo 侧用现成的 `LoadMCPServer`，
  或按 `AGENTGO_OCR_REMOTE` 走 HTTP。
- `scripts/release-win7.ps1` 已预置断言：产物不得出现 `onnxruntime`、`ppocrv6`，必须有 `ocr_image`、`visionocr`。
  负向标记目前没有任何构建含有它们，**D4b 落地时要用 `-tags onnx` 构建一次确认标记会出现**（见 §6.1）。

### 4.4 OCR 输出校验（所有构建都生效）

搬 WeKnora `application/service/ocr_sanitizer.go`（200 行），**已落在 `internal/visionocr/sanitize.go`**（见 §4.2.1）：
识别解码重复（`hasOCRRepetition`，≥512 rune 起判）、剔除「无文字内容 / no text」这类空回答、
去 markdown 围栏。它依赖 `html-to-markdown/v2`，AgentGo 没有这个包——用已有的 `golang.org/x/net/html`
剥标签即可，或直接跳过 HTML→Markdown 那一步（VLM 输出通常是纯文本或 Markdown，很少是 HTML）。

这道闸门对 VLM 路径尤其重要：VLM 会编、会重复，PP-OCRv6 不会。

### 4.5 待确认

**内网模型网关是否提供视觉模型？** 这决定主路径是否成立（**已确认：有**；真实网关上的识别质量仍未验证，见 §4.2.1）：

- 有 → OCR 走 VLM 远程，Win7 也可用，本地 ONNX 降为可选。
- 没有 → Win7 只能「无 OCR」或「局域网另起一台 Win10+ 做 OCR 服务」，后者要部署成本。


## 5. 许可与归属

- WeKnora 是 MIT（`LICENSE`：除列明的第三方组件外）。AgentGo 是 Apache-2.0。MIT 代码可以并入，但必须保留版权声明。
- 照现有约定（`licenses/crush/NOTICE.md` 的做法）：
  - 新增 `licenses/weknora/NOTICE.md`：MIT 全文 + Copyright (C) 2026 Tencent + **派生文件对照表**
  - 每个移植文件头部一行：`// Derived from WeKnora (github.com/Tencent/WeKnora), MIT. See licenses/weknora/NOTICE.md.`
- **不能沾的**：`gojieba`（CGO）、`html-to-markdown/v2`（若不想加依赖就自己用 `x/net/html` 剥标签；v1 先走后者）。
- PageIndex 的 Python 实现**一行都不复制**，只借范式；文档里注明出处即可。

## 6. 里程碑

每阶段只加「新包 + bridge 接线 + flag」，不动内核、不改 Protected 白名单。
**每个阶段的验收都要求两种构建全绿**：默认、`-tags web`；另有 `scripts/regress.ps1` 里「旧工具链 + CGO 关」的编译守卫。

| 阶段 | 内容 | 验收 | 估时 |
|---|---|---|---|
| **D0 ✅** | `internal/docchunk` 移植（11 个源文件 + 14 个测试文件）、arch 注册、`chunker` 插件、`memory` 可选切分钩子 + 回落 | 默认与 `-tags web` 全绿；157 个移植测试全过；真实 exe 冒烟（默认关，开关打开后挂上） | 已完成 |
| **D1** | `parse_file` 分块预览 + 页码偏移（`pageOffsets`）；snippet/normalize/chunkmerge 三个小件 | 一份 50 页 PDF 能返回「块 → 页码」；`JoinChunkContent` 的重叠合并有 golden 用例 | 1d |
| **D2** | `internal/docindex` + `doctree` 插件：建表、`index_document`、`search_document_tree`、`read_doc_node`、后台任务、降级 | 3 份长 PDF（>100 页）与现有 hybrid recall 对比命中率与 token 成本，用 `agentgo-eval` 出数；树建失败时回落正常 | 3–5d |
| **D3** | CSV/JSON 转换器进 `parse_file`；（可选）把 `visionocr.Validate` 接入 `vision_advisor` | `parse_file` 对 `.csv`/`.json` 返回表格而非原始文本；接入 `vision_advisor` 不在 OCR 本轮范围 | 1d |
| **D4a ✅** | `internal/visionocr`：provider 注册表 + VLM provider + 输出校验 + `ocr_image` 工具 + 插件接线 | 26 个测试全过（含 httptest 假网关）；默认与 `-tags web` 全绿；Win7 配方的产物断言「无 onnx 标记」与「有 ocr_image」 | 已完成 |
| **D4b** | `-tags onnx` 本地引擎 + `cmd/agentgo-ocr` sidecar（仅 Win10+ 离线场景） | `-tags onnx` 能识别一张中文截图；该构建的产物里 `onnxruntime` / `ppocrv6` 标记真的出现（发布脚本的负向断言才算有效） | 3–5d，按需 |
| **D5** | （可选）`sourceloc` 版面坐标，PDF 框选高亮 | — | 按需 |

## 6.1 D0 施工记录（2026-10-28）

### 做了什么

| 文件 | 内容 |
|---|---|
| `internal/docchunk/*.go`（14 个） | 从 WeKnora `internal/infrastructure/chunker` 移植；无构建标签 |
| `internal/docchunk/markdown.go` | 从 `docparser/image_resolver.go` + `markdown_image_scanner.go` 抽出 7 个纯字符串函数（`UnwrapLinkedImages` 及其依赖），去掉对 docparser 的依赖 |
| `internal/docchunk/docchunk.go` | `debugf` 替代 WeKnora 的 `logger.Debugf` |
| `internal/docchunk/service.go` | 新写的适配层：`Service.Chunk` 匹配 `memory.ChunkFunc`，把 `ContextHeader` 折进块正文 |
| `internal/docchunk/*_test.go`（15 个） | 14 个移植测试 + `service_test.go`；`parent_child_offsets_test.go` 删掉依赖 `sourceloc` 的那一条 |
| `internal/memory/chunker_hook.go` | `SetChunker` / `SplitContent` 钩子，**memory 不 import docchunk** |
| `internal/memory/hybrid.go` | `ChunkContent` → `chunkContent`（走钩子，取不到回落） |
| `internal/bridge/chunker_plugin.go` | `optionalPlugin("chunker")`，装钩子 + 提供服务；`AGENTGO_DOCCHUNK` 默认关 |
| `internal/bridge/chunker_plugin_test.go` | 三条：服务可达且钩子生效、Stop 后回到内置切分器、默认关闭 |
| `internal/arch/layers.go` | 注册 `"docchunk": LayerSystems` |
| `internal/arch/plugin_rules_test.go` | `concretePlugins` 加入 `docchunk`、`visionocr`，取代标签隐含的隔离 |
| `internal/config/flags.go` | `AGENTGO_DOCCHUNK`（默认 **0**）、`AGENTGO_DOCCHUNK_STRATEGY` |
| `scripts/release-win7.ps1` | 测试与构建都用 `-tags web`；产物断言：无 `onnxruntime` / `ppocrv6`，有 `ocr_image` / `visionocr` |
| `scripts/regress.ps1` | `-tags web` 构建 + 测试；另有「旧工具链 + CGO 关」的编译守卫（找不到工具链则警告并跳过） |
| `licenses/weknora/NOTICE.md` `LICENSE.md` | MIT 全文 + 派生文件对照表 + 「故意没搬什么」 |

### D0 落地时的实测

| 检查 | 结果 |
|---|---|
| 移植的 157 个顶层测试（399 个 RUN 含子测试） | 全过，`ok agentgo/internal/docchunk` |
| `internal/arch` 12 条架构守卫 | 全过（新包已注册） |
| 插件行为 | 服务在 bus 上；`memory.SplitContent` 与 `svc.Chunk` 输出一致；`Stop` 后回到 `ChunkContent` 输出 |

### 收回 `win7` / `nokb` 标签（同一天）

D0 最初是带 `//go:build !nokb && !win7` 落地的，还有一个 `chunker_plugin_none.go` 空桩。依据 §0.1 的实测改成无标签：
31 个文件去掉标签行、删掉空桩、补上 arch 规则、改两个脚本。改完之后重新验证：

| 检查 | 结果 |
|---|---|
| `gofmt` / `go vet`（受影响的 6 个包） | 干净 |
| 全量 `go test ./...` | 默认 75 个包、`-tags web` 76 个包，**全部通过，0 失败**（两轮合计 151 行结果，与 `go list` 的包数逐一对上） |
| arch 规则的反向验证 | 让 `memory` 临时 import `docchunk` 与 `visionocr`：`TestConcretePluginsAreOnlyReachedThroughTheAssembly` 失败并点名两个 import；撤掉后恢复通过 |
| 发布脚本的断言（把脚本里的原文抽出来执行） | 真实产物通过；人为塞入 `onnxruntime`/`ppocrv6`、去掉 `ocr_image`、塞入 wails 标记，三种都按预期抛错 |
| 真实 exe 冒烟（旧工具链、CGO 关、`-tags web`，数据目录隔离到临时目录） | 默认：28 个插件，无 `chunker`/`visionocr`；设 `AGENTGO_DOCCHUNK=1 AGENTGO_OCR=1`：两者挂上，日志 `[chunker] adaptive splitting on`、`[visionocr] provider=vlm` |
| `regress.ps1` 的守卫块（抽出原文执行） | 真实工具链通过；工具链不存在则警告并跳过；工具链构建失败则记入 `failed`；三种情况环境变量都还原 |

验证过程中抓到并修掉两个错误，记下来免得重犯：

1. `regress.ps1` 里 `"...at $Win7Go: skipping..."` 的 `$Win7Go:` 被 PowerShell 当成带作用域前缀的变量，
   整个脚本**解析失败**。改成 `'$Win7Go'`，让变量后面不再紧跟冒号。
2. 我第一次验证守卫块的方式本身有问题：在函数里 `Invoke-Expression`，`$failed +=` 写进了函数的局部变量，
   「构建失败也显示通过」是假象。改成在同一作用域里执行，才得到可信结果。

**还有一条断言没有被真实触发过**：`onnxruntime` / `ppocrv6` 两个负向标记是为 D4b 预置的，目前没有任何构建含有它们，
只用人为塞入字节的方式验证了检查逻辑。D4b 落地时，必须用 `-tags onnx` 构建一次，
确认这两个标记真的出现在产物里，这条断言才算数。

### 断言标记的教训

第一版发布脚本的断言用 `adaptive document chunker`、`ChunkOverlap`、`breadcrumb` 这类词，实测三者在**不含该包**的产物里
仍然存在——因为 `config/flags.go` 的 flag 说明文字始终参与编译。改用只存在于 `docchunk` 内部的符号
（`splitBySeparators`、`HeadingHierarchy` 等）并双向验证后才可信。
标签收回后知识库不再需要产物断言；**新增或改动断言时必须这样验：该有的构建里有，不该有的构建里没有，不能靠猜。**

### A/B 数据（切分质量）

同一份带章节的中文文档（约 1.4k 字，512/80）：

```
[0] 440 runes | header="# 第一章 概述"
[1] 311 runes | header="# 第一章 概述\n## 1.1 背景"
[2] 281 runes | header="# 第一章 概述\n## 1.2 目标"
[3] 380 runes | header="# 第二章 风险"
```

切分点落在标题边界，每个块自带所属章节路径。内置切分器按 `\n\n` 切，块内混章节且无面包屑。

**但召回效果还没测。** `AGENTGO_DOCCHUNK` 保持默认 0，等 D1 做完页码偏移、
能用 `agentgo-eval` 跑 A/B 之后再决定是否改默认。`docs/eval-baseline.md` 的 85–88% 缓存命中
是在旧切分器下测的，改默认前必须重测。

### 旗标（全部加进 `internal/config/flags.go`，`GroupPlugins`）

旗标决定「这次跑不跑」；三个插件在每个构建里都存在，旗标默认全是 0。

```
AGENTGO_DOCCHUNK               bool   默认 0  使用移植的切分器；0 回到旧实现（见 §7）
AGENTGO_DOCCHUNK_STRATEGY      string 默认 "" auto/heading/heuristic/recursive/legacy
AGENTGO_DOCTREE                bool   默认 0  文档树插件（D2）
AGENTGO_DOCTREE_MODEL          string 默认 "" 建树摘要用的模型；空 = AGENTGO_REWRITE_MODEL
AGENTGO_DOCTREE_MAX_NODES      int    默认 400 单文档树节点上限
AGENTGO_OCR                    bool   默认 0  OCR 总开关（D4a 已落地）
AGENTGO_OCR_PROVIDER           string 默认 "" vlm | remote | onnx（空 = 自动挑选可用者）
AGENTGO_OCR_VLM_MODEL          string 默认 "" VLM 模型名；空 = VISION_MODEL，再空 = 主模型
AGENTGO_VISION_MODEL           string 默认 "" 图像理解模型；空 = 主模型（D4a 已落地）
AGENTGO_OCR_TIMEOUT_SEC        int    默认 120 单次 OCR 超时（D4a 已落地）
AGENTGO_OCR_REMOTE_URL         string 默认 "" 远程 OCR 服务的 base URL（D4b）
AGENTGO_OCR_SIDECAR            string 默认 "" agentgo-ocr.exe 路径；空 = 不拉起（D4b）
AGENTGO_OCR_MODELS             string 默认 "" 本地模型目录（D4b，仅 -tags onnx）
AGENTGO_OCR_SANITIZE           bool   默认 1  校验输出（已并入 visionocr.Validate，恒开启）
```

D0 已落地前两条，D4a 已落地标注了「已落地」的几条；其余随对应阶段加入。
`AGENTGO_OCR` 与本地 ONNX 无关：Win7 上 VLM / 远程 provider 可用，只有 `onnx` provider 被 `-tags onnx` 与 CGO 门控。
`AGENTGO_DOCTREE` 默认 0：原先计划默认 1，是因为它被标签挡在 Win7 之外；标签收回后，
为了让所有构建的默认行为都等于今天，改为默认 0，等 D2 的对比评测出数再决定。

## 7. 需要留意的行为变化

1. **记忆召回会变**：`ingestRecord` 从「1200 字符 / 200 重叠」换成「512 / 80 + 标题面包屑」。
   这是切分质量升级，但会改变既有召回结果与评测基线（`docs/eval-baseline.md` 的缓存命中 85–88% 也是在这个前提下测的）。
   所以 `AGENTGO_DOCCHUNK` **先默认 0**，D0 合入后跑 A/B，出数再改默认值。
   **Win7 同样不受影响**：`docchunk` 虽然编进了产物，开关默认 0，永远走旧路径，基线继续成立。
2. **`ContextHeader` 进 embedding**：原包 `Chunk.EmbeddingContent()` 会把面包屑拼进被向量化的文本。
   AgentGo 的记忆召回是 FTS5 优先，得确认面包屑不会污染 FTS 命中（大概率是有利的，但要测）。
3. **建树的 token 成本**：PageIndex 自报约 $0.001/页。内网模型便宜，但长文档首次建树仍会花几分钟，
   必须放在后台任务里、可取消、失败可重试——复用 `taskhub` 而不是自己造队列。
4. **维护成本在工具链，不在标签**：没有 `win7` / `nokb` 之后，Win7 构建与别的构建是同一份源码，
   剩下的风险是「新依赖在旧工具链下编不过」或「新代码要 CGO」。`scripts/regress.ps1` 里
   「旧工具链 + CGO 关」的编译守卫让这条成本每天被自动付掉，而不是等发版才发现。
5. **OCR 不是 Win7 的禁区，ONNX 才是**：`visionocr` 包本身在所有构建里都存在，
   VLM 与远程 provider 在 Win7 上可用；只有 `onnx_*.go` 受 `-tags onnx` 与 `cgo` 双重约束。
   别把整个 OCR 能力当成知识库的一部分排除掉。
6. **「编进去但关着」的启动成本不是 0**：`docchunk` 包初始化约 1 ms、分配 1.2 MB（26 处包级正则），
   开关关着也每个构建都要付。这个量级不值得去改被移植的代码；若将来要压到 0，
   把包级 `regexp.MustCompile` 改成惰性（`sync.Once`）即可，行为不变。

## 8. 一句话

把 WeKnora 当作「算法素材库」而不是「要对接的系统」：切分器、检索后处理、OCR 校验、原文定位四块直接吃进来，
做成三个原生插件；文档树补上 PageIndex 的检索范式与页码引用；OCR 走可选构建 + sidecar。
**一条分支、一份源码、按能力组装**：构建标签只留给编不过的东西（`web`、`milvus`、将来的 `onnx`），
知识库与 OCR 用默认关的开关控制。Win7 的发布配方是「旧工具链 + CGO 关 + `-tags web`」；
在真实 Win7 机器上的验收仍待做。
