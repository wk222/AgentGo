# 办公文档工具（Excel / PPT / Word）：计划、取舍与限制

日期：2026-10-26　分支：`win7`（之后合入 `master`，桌面版和 Win7 版共用）

## 为什么这样做

办公电脑只有内网，Go / Python / Node 都可能下载不了。所以**全部编进 exe**：不用 `go run`，不依赖
Python、Node、Office、WPS 或 LibreOffice；生成的文件用 Office 和 WPS 都能打开。外部项目只借鉴思路，
没有复制代码或文字（见 `licenses/office/NOTICE.md`）。

## 评估过的 5 个项目

| 项目 | 结论 |
| --- | --- |
| excelize | 已是依赖，升到 v2.11.0，做成 `read_xlsx` / `edit_xlsx` |
| presentation-skill | 只取思路：先大纲、标题写结论、生成后检查版面；用 Go 重写成 `create_pptx` / `inspect_pptx` |
| deepseek-harness skill-office | 只取结构：一个路由技能 + 每种文件一个技能 + 生成后验证；用中文重写，调用我们自己的 Go 工具 |
| openai/skills | 只参考 `SKILL.md` 的写法 |
| cxuu/golang-skills | 办公场景用不上，暂不集成（以后要做 Go 编码技能包再说，需保留署名） |

## 做了什么

| 工具 | 读/写 | 要点 |
| --- | --- | --- |
| `create_docx` | 写 | 已有（Markdown → Word） |
| `create_xlsx` | 写 | 已有；现在写出公式的缓存结果和正确的 `<dimension>` |
| `read_xlsx` | 读 | 列所有工作表、按区域分页读、按文字查找；**公式先算出结果**；日期/百分比按显示格式返回；**列出工作表上已有的图表**（锚点、类型、标题、各系列取数区域） |
| `edit_xlsx` | 写 | 15 种操作（改格/区域、清空、增删改名工作表、增删行、格式、列宽、合并、冻结、筛选、增加/删除图表）；整批全成功才保存；就地改先留带时间戳的备份；可 `save_as` |
| `create_pptx` | 写 | 8 种内容版式（bullets、two_column、table、chart、stats、timeline、image、section）+ 封面/结束页；6 种原生图表（column、stacked_column、bar、line、pie、doughnut）；长表自动拆页；演讲备注；5 套主题；文字按版框自动选字号 |
| `inspect_pptx` | 读 | 每页版式/标题/文字/表格/图表数据/图片数/备注；检查缺标题、文字溢出、字号 < 10 pt、越界、重叠、空页、过密、缺替代文字 |

### 关键实现

- **PPTX 用标准库手写 OOXML**（`pptx*.go`）：16:9、真正的标题/正文占位符、母版/版式/主题/备注母版齐全，
  图表是原生的并内嵌工作簿（PowerPoint 里能“编辑数据”）。文字宽度用字符类别估算（CJK = 1 em，ASCII 按词折行，
  行高 1.35），宁可估宽，保证版框里有余量。
- **公式缓存**（`xlsx_cache.go`）：excelize 写公式单元格不带结果，微信/资源管理器/手机预览里会是空白。保存后在 zip 里
  逐个公式单元格补上 `<v>` 结果（数字、布尔、错误、文本），并设置 `fullCalcOnLoad`，Excel/WPS 打开时仍会重算。
  编辑过的公式总是重新计算，不会留下旧值；算不出来的函数不写值，而不是写错值。
- **已有图表的读与删**（`xlsx_charts.go`、`xlsx_rels.go`）：excelize 没有读图表的接口，所以直接读包里的
  工作表 → 绘图 → 图表部件，得到 `charts`。`delete_chart` 按锚点删除；`add_chart` 遇到同一锚点已有图表时报错，
  不会叠一个新的上去。这是 e2e 暴露的问题：改了数据后模型既看不到旧图表、也删不掉，结果旧图停在旧范围，
  又叠了一个新图。Apache POI / openpyxl 写的文件把关系目标写成绝对路径（`/xl/drawings/drawing1.xml`），
  excelize 的 `DeleteChart` 遇到这种文件**什么都不做也不报错**，所以打开时先把这类目标改成相对路径。
- **新插入的行沿用相邻行的格式**（`inheritRowStyle`）：和 Excel 一致，数字格式、边框不会丢；
  紧挨表头（第 2 行）插入时沿用下面一行，在第 1 行之前插入则不复制。
- **图表坐标轴文字**：excelize 默认把坐标轴文字写成 `#D9D9D9`（在白底上几乎看不清），`add_chart` 显式设成 `#595959`
  并打开横向网格线。PPT 的图表是自己写的 XML，一直用主题文字色，不受影响。
- **读写分级治理**：`read_xlsx`、`inspect_pptx` 是静态可见 + 不改文件 + LOW，只读画布里也能用；其余四个是 MEDIUM、走审批。
- **内置技能**：`internal/skills/builtin/` 用 `go:embed` 编进二进制；工作区同名技能优先；`AGENTGO_OFFICEDOC=0` 时隐藏。
  默认配置下 Eino 的技能中间件是关的（`AGENTGO_EINO_SKILL_MW`），模型唯一的入口是 `activate_skill`；
  它以前只回一句描述，现已改为返回全文（上限 16000 字节，按字符边界截断），找不到时列出可用 id。
  写字工具的描述里也点名了对应技能。

### SVG 自由版式（`create_pptx` 的 `svg` 版式）

对齐 [ppt-master](https://github.com/hugohe3/ppt-master) 的思路：让模型画受约束的 SVG（绝对坐标的二维矢量，和 DrawingML 几乎同构），
再确定性地翻译成原生可编辑的 PowerPoint 对象，而不是让模型直接拼 PPTX XML，也不是插图片。**用 Go 重写，没有拷贝它的代码**；
它的 Python 转换器（`svg_to_pptx`，仅 builder.py 就 342 KB）依赖 Python 3.10+ 和一批 pip 包，Win7 办公机跑不了。

```
模型 → svg 字段（受约束 SVG）
     → svg_doc.go    严格 XML 解析 + 元素白名单 + 颜色/样式解析（一次列出全部问题和行号，模型据此整页重发）
     → svg_geom.go   仿射变换栈、路径数据（M L H V C S Q T A Z → 绝对 M/L/C/Z，圆弧转三次贝塞尔）、精确包围盒
     → svg_scene.go  Scene IR：rect / ellipse / line / path / text / image / group，已摊平到页面英寸坐标
     → svg_write.go  DrawingML：p:sp（prstGeom / roundRect / custGeom）、p:cxnSp、p:grpSp、p:pic、渐变、阴影、箭头、文字
     → pptx_svg.go   接进现有的 deck 打包（OPC 由 pptx.go 的标准库实现负责，母版/主题/备注共用）
```

- **没有引入** [hurtener/pptx-go](https://github.com/hurtener/pptx-go)（3.1 万行）或 [mmonterroca/pptxgo](https://github.com/mmonterroca/pptxgo)（0.9 万行，
  无 custGeom）：我们已有自己的 OPC/PPTX 写入层，缺的正是 SVG→Scene→DrawingML 这一段，两个库都不提供；hurtener 的 `scene`
  是声明式组件树（卡片、时间线），不是 SVG 摄入，它只有一个内部的 SVG 路径转 custGeom 用于图标。只读过它们的结构，未使用其代码。
- **白名单**：`svg g defs rect circle ellipse line polyline polygon path text tspan image linearGradient radialGradient stop marker filter(feDropShadow) title desc`。
  `style`、`use`、`symbol`、`clipPath`、`mask`、`pattern`、`foreignObject`、其他滤镜、动画、脚本、外链图片、DOCTYPE/ENTITY 一律拒绝并给出替代做法。
  只会改变观感却无法翻译的属性报错；无害的属性忽略，影响观感但可近似的给 warning（如渐变描边取首色）。
- **主题色名**：`primary secondary accent surface text muted line soft chart1..6` 可直接当颜色用，和其他页自动同调。
- **文字**：SVG 文字不自动换行，所以每个 `text` 转成不换行的文本框，一个 `tspan` 一行；同一左边缘的连续行合成一个框，
  行距用“固定行距（`spcPts`）”写出，与字体自身行高无关。`id="title"` 的文字成为真正的标题占位符。
  基线校准：WPS 里固定行距下基线在行框的 0.81 倍处，PowerPoint 据信是“行框底部上方一个字体下沿”；两者在行距等于自然行高（1.31 em）时重合，
  否则相差 0.19×(行距−1.31 em)。取两者的中点，整块文字最多整体偏 0.1×(行距−1.3 em)（32 px 字、60 px 行距时不到 2 px），行距本身不变。
  WPS 实测校准页（24/32/48/16 px，行距 24–80 px）基线误差均在 ±1 px。
- **分组**：带 `id` 的 `g` 或多个子元素的 `g` 成为 `p:grpSp`（子元素用页面坐标，`chOff/chExt` 与 `off/ext` 相同），在 PowerPoint 里可整体移动。
  铺满整页、最先画的 `rect` 成为页面背景。
- **变换**：矩形/椭圆/图片/文字支持平移、等比缩放、旋转（写成 `rot`）；斜切或镜像的矩形/椭圆退化为自由曲线；路径任意仿射。
- 上限：400 KB、4000 个元素、嵌套 20 层、1500 个形状、坐标绝对值 ≤ 1e6、渐变 2–10 个色标（DrawingML 的限制）。

### 套用公司模板（`create_pptx` 的 `template`）

用户常有一份公司标准模板（logo、标题栏、背景图）要求“按这个做”。自己画一套外观永远不像，所以让生成的稿子直接**长在模板的母版和版式上**。
没有引入任何库：模板包用 `archive/zip` 读，部件用正则和 `encoding/xml` 摸，和 `.pptx` 写入层共用 OPC 代码。

```
inspect_pptx(layouts=true)   读模板：版式编号/名称/占位符位置、版式上的 logo 和背景图（decor）、样例页用了哪些版式、品牌色、正文可用区域
create_pptx(template=...)    pptx_template.go       分析：loadTemplate → analyze → chooseDefaults（封面/内容页版式）、品牌色、内容区
                             pptx_template_slide.go 每页：使用模板版式；标题/副标题/页码是版式占位符（空 spPr，位置和字体继承）
                             pptx_template_pack.go  打包：从根关系做可达性搜索，只带走母版/版式/主题/媒体等部件，丢掉模板自己的页
```

- **部件原样搬运**：可达部件逐字节复制（母版、版式、主题、媒体、tags、presProps、备注母版……）。重新生成的只有
  `[Content_Types].xml`、`_rels/.rels`、`docProps/core|app.xml`、`ppt/presentation.xml` 及它的关系。可达性搜索跳过 presentation→slide 的关系
  （所以模板自带的页、备注、图表、照片都不进新稿），也跳过根上的缩略图和自定义属性。`presentation.xml` 里删掉 `sldIdLst`、`custShowLst`
  和章节扩展（它们指向被丢掉的页）后重插新的 `sldIdLst`；模板没有备注母版时才补我们自己的。
- **版式怎么选**：编号是 `slideLayoutN.xml` 的 N，也可以写名称；名称重名时先看模板样例页用了哪个，仍然歧义就报错并列出编号。
  默认：模板有样例页时，封面 = 首页版式，内容页 = 中间页里最常用的有标题占位符的版式；没有样例页时按版式 `type`（`title`，`titleOnly`，`obj`）。结束页默认同封面。
- **封面/结束页字体**：模板里这类版式经常只给位置、不给颜色字体（标准 Office 版式），直接用会是黑色宋体。版式的标题占位符没有自己的颜色时，
  才加粗、改成品牌色，并取 54/48/44/40/36 pt 中一行放得下的最大字号；版式已有颜色的一律不动。
- **品牌色**：内容页版式标题占位符 `lstStyle` 里的 `srgbClr`（太接近白色的不要）。没写 `theme` 时，图表、卡片、`svg` 里的主题色名都按它生成配色
  （`brandPalette`），所以生成的图形和模板标题同色；写了 `theme` 则以 `theme` 为准。
- **内容区**：面积不小于页面 30% 的正文/对象占位符定义内容区；没有时取标题下沿加 0.25 英寸到页底 0.4 英寸。所有标准版式都按它排版（`deck.fr`），
  `inspect_pptx` 以 `content_box_px` 给出，`svg` 页据此作画。版式上的 logo、图片标 `keep clear`；`svg` 的图形盖住它们超过 25% 时给 warning（只提醒，不强制避让）。
- **没有标题占位符的版式**：标准版式报错并提示换版式或用 `svg`；`svg`、`section` 仍可用（此时 `svg` 里 `id="title"` 的文字保留为普通文本框）。
  有标题占位符时，`svg` 里的标题文字丢掉并 warning，标题由占位符承载。
- **图片重名**：我们的图片和模板媒体同名时改名为 `agentgo%d-<name>`。模板版式里已有 `ppt/charts/chartN.xml` 时加图表会报错（不覆盖模板部件）。
- 模板模式下不画页脚、装饰线和自己的页码框；页码用版式的页码占位符。

#### 内置模板（`template: "builtin:<名字>"`）

办公电脑上没有模板文件时也要能出公司风格的稿子，所以把模板编进 exe（`builtin_template.go`，`go:embed builtin_tpl`）。
`create_pptx` 的 `template` 和 `inspect_pptx` 的 `relative_path` 都认 `builtin:company`，其余流程和工作区模板完全一样。

- **不入库**：模板带公司 logo 和背景图，`.gitignore` 忽略 `internal/officedoc/builtin_tpl/*`，只有 `README.txt` 入库
  （`go:embed` 要求目录里至少有一个文件，全新克隆也能编译）。没有模板文件的构建里 `builtin:company` 返回
  “none in this build”，技能据此让模型改向用户要模板文件。`scripts/release-win7.ps1` 打包前会列出带了哪些模板，没有就警告。
- **怎么做出来**：`BUILTIN_TPL_SRC=<公司 deck> go test ./internal/officedoc -run TestMakeBuiltinTemplate`（见 `builtin_tpl/README.txt`）。
  `stripTemplate` 保留母版、版式、主题和它们用到的媒体（逐字节），去掉全部内容：
  - 每个样例页换成同一版式上的**空页**（这样默认封面/内容版式、`used_by_slides` 仍照原稿推断，不需要另存清单）；
  - 备注、图表、照片、批注作者、修订历史、`customXml`、文档属性全部丢掉；
  - `ppt/tags` 保留但清空（WPS/Office 会把机器和用户的标识放在里面，如 `COMMONDATA` 里的 `hdid`；幻灯片和演示文稿指向它们，不能直接删）。
- **核对**：用原稿所有幻灯片和备注里的 54 段文字去搜生成的模板，没有命中；母版/版式里只剩“单击此处编辑母版标题样式”这类通用提示；
  搜 `eyJ`（base64 的 JSON）、`COMMONDATA`、`hdid`、`userid`、`lastModifiedBy`、`@` 均无。
  那份 4 MB 的公司 deck 变成 296 KB（67 个版式、3 套母版、8 张图片，约 150 KB）。
- `inspect_pptx` 读内置模板时固定带 `layouts`，不逐页描述（空页会报一堆“空白页”）并给一条说明。
- 测试（`builtin_template_test.go`，不依赖真实模板）：带批注作者、修订历史、公司名、标签标识的假公司 deck 被清干净且结构良构、
  版式逐字节保留、默认版式不变；`builtin:` 在 `create_pptx`/`inspect_pptx` 里和工作区模板一样用（大小写不敏感）；各类错误
  （名字不存在时列出现有的、没有模板、名字里带路径、文件损坏）；真实内置模板存在时端到端生成并检查结构（没有时跳过）。
  反向试验：不再丢批注作者，测试立刻失败。

## 限制（如实）

- 没有在真正的 PowerPoint / Excel / Word 里验证过。已验证的是：自带的结构检查（部件良构、内容类型、关系目标、
  形状 id 唯一）、python-pptx 1.0.2 与 openpyxl 3.1.5 读回、**WPS 通过 COM 逐页导出 17 张 PNG 目视检查**。
  需要有人在 PowerPoint 里打开 `OFFICEDOC_EXPORT_DIR` 导出的样例看一眼。
- 只支持 `.xlsx` / `.pptx` / `.docx`；不支持 `.xls` / `.ppt` / `.doc`、带宏的 `.xlsm`、加密文件。
- `edit_xlsx` 是“读入再写出”：数据透视表、切片器、宏、外部链接、个别图表样式可能丢失；技能里要求先告知用户并 `save_as`。
- **增删行不会改图表**：excelize 的 `InsertRows` / `RemoveRow` 会调整公式引用（和 Excel 一样），但不移动图表、也不改图表的取数区域。
  要让图表跟上数据，只能 `delete_chart` 再 `add_chart` 重建，重建出来是默认样式，原图的颜色、字体不保留。技能里写明了这个步骤，
  并让模型先看 `charts` 再决定。
- 删掉的图表，它的部件仍留在文件里（不再显示，WPS 实测打开正常）。这是有意保留的：excelize 给新图表编号用的是“现有图表部件数 + 1”，
  若清掉中间的部件，之后再 `add_chart` 会得到一个已被占用的编号，把另一张图表覆盖掉。
- 在汇总区域紧下方插入行时，`=SUM(B2:B3)` 这类范围不会自动扩大（Excel 也是这样），要自己改公式；技能里写明了。
- 不能原位修改 `.pptx`，也不能读写已有的 `.docx`；PPT 只能读出内容后重新生成（母版、动画、自定义版式不会带过去）。
- `create_docx` 不插入图片和图表（图片只留 `[image: 说明]` 文字）。
- `svg` 版式：文字宽度靠估算（汉字≈1 字号、英文≈0.55 字号），不是真实字形度量；估算会让“靠右/居中”的文本框略宽，但文字本身位置不受影响。
  没有 `filter` 的模糊/混合、没有 `clipPath`/`mask`/`pattern`/`use`；描边渐变只取首色；`fill-rule="evenodd"` 被忽略。
  PowerPoint 里的文字基线位置未实测（见上，按两种应用的模型取中点），WPS 实测在 ±1 px。
- 模板模式：版式只能沿用，不能新建，也不会复制模板样例页上的文字和图片；模板里没用到的母版和版式都保留，所以输出大小取决于模板
  （一份 4 MB 的公司模板出 330 KB）。内容区是启发式的，logo 和背景图只做 warning 不强制避让（例如图表的坐标轴文字可能压到背景波纹上）。
  标题长度的 warning 按 24 pt 估算，不读版式里的真实字号。没在真正的 PowerPoint 里打开过。`.potx` 模板的主部件内容类型是“模板”，新稿里统一改成“演示文稿”（有测试），否则 PowerPoint 会把新稿当模板打开。
- 看不到真实渲染时，`inspect_pptx` 的溢出判断是**估算**（容差 8%），不是排版引擎的结果；同样的估算器也用于生成，
  所以它能发现手工改过的文件，但不能证明自己的生成结果在所有字体下都不溢出。
- 没检测 LibreOffice 的渲染/PDF 导出（可选项，未做）。

## 验证记录

- 单元测试：`internal/officedoc`（含 `export_test.go`、`pptx_inspect_test.go` 及 `testdata/python-pptx.pptx`
  这个由 python-pptx 生成的第三方样例）、`internal/skills`、`internal/tools`（`activate_skill`）、
  `internal/bridge`（插件治理 + 端到端：建表 → 读 → 改 → 读 → 建 PPT → 检查）。
- `skills_doc_test.go`：技能文字里的工具名/字段名/版式名必须真实存在，工具的每个选项和版式必须在技能里有说明。
  （做过反向试验：往技能里写一个不存在的版式，测试会失败。）
- 整个仓库 `go test ./...` 在默认标签和 `-tags web` 下都通过（legacy Go，各 52 个包 ok）。
- 真实大模型（OpenAI 兼容端点）端到端，用 `agentgo-cli run -yolo` 在隔离的数据目录里跑：
  - PPT：多次“按需求写大纲 → `create_pptx` → `inspect_pptx` → 按提示修”，成品用 WPS 逐页导出图片目视检查。
  - Excel：读 → 改 → 读回。其中“设备金额改成 500、新增一行、合计和占比跟着变、图表也要包含新行”这一条，
    模型先读到 `charts`，同一批操作里 `delete_chart` + `add_chart` 重建，读回确认只剩 1 张图、范围含新行，最后给出
    合计 1,250 与各项占比 48% / 40% / 12%。
  - 改完的工作簿用 WPS 表格（COM）打开：1 张图表，系列区域含新行，坐标轴文字 `#595959`，新行数字格式和相邻行一致；
    openpyxl 也能读回。
- `svg_test.go`：路径解析（含无分隔符的圆弧标志、小半径放大、S/T 反射）、圆弧点落在圆上、曲线极值包围盒、变换组合、颜色与主题色名、
  文字基线/行距（含字号混排）、变换摊平、背景识别、一次报全部问题、坏文档（DOCTYPE、非 16:9、深度/形状数上限）、图片（工作区、data URI、越界/远程拒绝）、
  3000 条随机路径数据 + 一组畸形输入不 panic、技能里的示例 SVG 必须能转换。
- SVG 版式的渲染核对：WPS COM 导出校准页，用 Pillow 量文字基线（24/32/48/16/64 px、行距 24–80 px，误差 ±1 px）；
  渐变、阴影、圆角、虚线、箭头、圆弧、贝塞尔、alpha 肉眼检查；圆弧图标与 Chromium 渲染同一段 path 对照一致。
- 真实模型（本机默认的 Azure 部署，`agentgo-cli run -yolo`，隔离数据目录）：读 `activate_skill` 的 office-pptx 后，一次写出
  2 页 svg（四步流程图、三张 KPI 卡片）+ 1 页 bullets，`create_pptx` 一次通过，`inspect_pptx` 无 issue；WPS 导出后版面干净。
  只跑了这一个场景、一个模型，不代表其他模型的成功率。
- 模板模式（`pptx_template_test.go`）：用我们自己的写入器造一份“公司模板”（品牌色标题、右上角 logo、缩略图、章节列表、样例页含图表和备注），
  再在它上面生成：版式和母版逐字节保留、模板自己的页/备注/图表/嵌入表格/图片/缩略图/章节都不进新稿、备注复用模板的备注母版（没有时才补）、
  标题/页码是占位符且不覆盖字号颜色、封面按品牌色加粗、默认版式选择（有无样例页两种）、重名版式的歧义报错、图片重名改名、
  `inspect_pptx layouts=true`（版式编号、decor 的 keep clear、`content_box_px`）、`.potx` 输入、各类错误（找不到文件、扩展名不对、覆盖模板自身、没有标题占位符）。
  反向试验：让可达性搜索不再跳过模板的页，测试立刻报“chart1.xml、嵌入表格、image1.png 应该被丢掉”。
- 模板模式的真实文件：一份约 4 MB 的公司标准模板（带 logo 和背景图；模板含 9 张样例页、3 套母版、67 个版式），不入库。
  生成 330 KB 的新稿，WPS 打开 8 页；封面、目录、要点、原生图表、数字卡片、表格、`svg` 流程图都落在模板的内容区内，
  标题、logo 和页码和原模板一致。真实模型（本机默认 Azure 部署，`agentgo-cli run -yolo`）读技能后先 `inspect_pptx layouts=true`
  看版式，再 `create_pptx template=...`，一次生成 7 页（含一页 `svg` 流程图并把标题交给标题占位符），`inspect_pptx` 无 issue；WPS 导出核对版面干净。
  只跑了这一个模板、一个模型，不代表其他模板和模型的成功率。
- 重新导出样例：`OFFICEDOC_EXPORT_DIR=<导出目录> go test -run TestExportSamples ./internal/officedoc`。

## 经验

- excelize 2.11 的 `Chart.Title` 变成 `ChartTitle{Paragraph: []RichTextRun}`；对公式单元格 `SetCellValue` 会删掉公式；
  `GetCellType` 对普通数字返回 Unset；公式单元格没有 `<v>`。读取时必须先看公式再看值。
- 技能正文旧的截断是按字节切 4000，中文会被切在字符中间（乱码）。已改成按字符边界、上限 16000。
- 仓库的 `.gitignore` 默认忽略所有 `*.md`（只放行 README 和 `docs/`、`licenses/`），而内置技能是 `go:embed builtin/*/SKILL.md`。
  不加例外的话，提交后全新克隆会因为找不到这些文件而无法编译。已加 `!internal/skills/builtin/**/*.md`。
  以后凡是被 `go:embed` 的文件，都要用 `git status` / `git check-ignore -v` 确认没被忽略。
- 同一个文件不要并行发多个编辑调用，会出现 `ftruncate` 失败，需要先重读确认文件状态。
- excelize 的图表：`DeleteChart` 只删绘图里的锚点（图表部件和关系还在）；关系目标是绝对路径时它什么都不做；
  新图表编号 = 现有图表部件数 + 1，所以部件编号一旦有空洞，下一张新图会覆盖已有的。`WriteToBuffer` 中途调用不影响后续编辑，
  所以判断“现在这个工作表上有什么图表”可以先序列化再读。
- 有些问题不在办公工具里。e2e 里偶发服务商 400（`tool_calls` 后面没有紧跟 `tool` 消息），模型被降级成“[能力降级]”的无工具对话。
  根因在会话脊柱（`session_spine_reminders.go`）：`restoreSystemReminders` 按“距末尾第几条”把提醒插回历史，而每次调用前投影会在
  末尾多追加一条 `runtime` 上下文消息，位置被挤歪，提醒落在工具调用和它的结果之间。只在“非 DeepSeek 模型（工具搜索提醒是真正的
  system 消息）且运行上下文非空”时触发。修法：计数只算真正的对话消息，并保证不会插进一组 `tool_calls` / `tool` 之间；
  回归测试 `TestSpineReminderNeverSplitsToolCallFromItsResult`、`TestRestoreReminderStepsBackOverToolResults`（修复前会失败）。
  用旧/新两个二进制各跑真实端点对照过：旧的降级，新的正常。
- 用 COM 驱动 WPS 检查文件时，`New-Object -ComObject KET.Application` 开的是独立实例，用完自己 `Quit`，不会碰用户已经打开的窗口；
  不要用 `Marshal.GetActiveObject`（pwsh 7 没有）去抢用户的实例。

## 后续（未做）

1. 前端：生成的 .pptx 在界面里预览缩略图（现在只能靠 `inspect_pptx` 的文字结果）。
2. 在 PowerPoint 里人工验收样例；在真实 Win7 上验证整套（包括这些工具）。
3. `.pptx` 原位编辑（替换文字、改图表数据）和 `.docx` 读取/局部替换。
4. 检测到 LibreOffice 时可选的渲染/PDF 导出。
5. Go 编码技能包（cxuu/golang-skills），保留署名。
