# office-pptx

用 `create_pptx` 生成、`inspect_pptx` 检查 PowerPoint 演示稿：工作汇报、复盘、方案、培训。先定大纲再动手，每页说一个结论。

## 动手之前：定三件事

1. **谁看、什么场合**：领导汇报、团队例会、对外介绍，决定详略和语气。
2. **讲多久**：一页大约讲一到两分钟，十分钟的汇报做 6 到 10 页，半小时 15 到 20 页。
3. **一句话结论**：这份稿子看完要让人记住什么。写不出来就先和用户把它问清楚。

数据以用户给的为准。没有的数字不要编造：留空、写"待补充"，或者让用户补一份数据后再做。

路演、融资、发布会、立项答辩、述职这类要说服人的稿子：先读 `skill:office-pitch-deck`，再回到这里生成。

## 先写大纲，再生成

- **标题写结论，不写话题**：不是"网络覆盖情况"，而是"三季度覆盖率升至 98.6%，较二季度提升 0.4 个百分点"。每页一行标题。
- 叙事顺序：先结论，再证据（图、表、数字），最后行动和下一步。
- 封面和结束页由工具生成：`title`、`subtitle`、`author`、`date` 填在顶层，`closing` 写结束页文字。`slides` 里只放内容页。
- 一页只讲一件事。超过 7 个要点就拆页，或者改用表格、图表。

## 选版式

| 内容 | `layout` | 要点 |
| --- | --- | --- |
| 几条要点 | `bullets` | 最多 7 条，每条一两行；`**文字**` 加粗数字；以 `- ` 开头是下级要点 |
| 对比：优劣、前后、两个方案 | `two_column` | `left`、`right` 各有 `heading`，加 `bullets` 或 `text` |
| 2 到 6 个关键数字 | `stats` | `value` 写大数字，`label` 写含义，`note` 写对比，如"较上季 +0.4pt" |
| 趋势、对比、占比 | `chart` | 见下；可加 `takeaway`（最多 4 条）写在图旁边 |
| 明细数据 | `table` | `columns` 不超过 8 列；行太多会自动拆成"（1/3）"几页；`caption` 写数据来源 |
| 流程、计划、里程碑 | `timeline` | 2 到 6 步，每步 `title` 加一两句 `detail` |
| 截图、示意图 | `image` | `image` 是工作区里的 png/jpg，`caption` 写图注 |
| 章节分隔 | `section` | `title` 加一行 `body` |
| 流程图、架构图、关系图、带图标的信息卡片 | `svg` | 标准版式画不出来时才用，见下面"自由版式" |

图表类型：`line` 看趋势；`column` 比较几个类别；`bar`（横向）用于类别多或名称长；`stacked_column` 看构成；`pie`、`doughnut` 看占比，类别不超过 6 个，只用第一个系列。`categories` 是横轴或扇区名称，每个系列的 `values` 个数必须等于类别数，`unit` 写单位（万元、%）。图表是原生的，用户在 PowerPoint 或 WPS 里可以直接改数据。

数字能进图表或表格的，不要写成长句；长文字放进 `notes`（演讲备注）。`notes` 适合放讲稿、口径说明和数据来源。

## 自由版式：`svg`

标准版式画不出来的页（流程图、架构图、关系图、自定义信息卡片）用 `layout: "svg"`，在 `svg` 字段里写**一个**受限的 SVG。工具把它转成 PowerPoint 里能直接编辑的形状、文字和曲线，不是图片。能用标准版式表达的内容不要用 `svg`：标准版式更稳，也省字数。`svg` 页和其他版式可以混排，封面和结束页仍由工具生成。

画布固定为 `viewBox="0 0 1280 720"`（16:9，单位是像素）。

| 可以用 | 说明 |
| --- | --- |
| `rect`、`circle`、`ellipse` | `rect` 的 `rx` 是圆角；第一个画、铺满整页的 `rect` 会成为页面背景 |
| `line`、`polyline`、`polygon`、`path` | `path` 支持 M L H V C S Q T A Z，大小写都行，含圆弧 |
| `text`、`tspan` | 文字**不会自动换行**：一行写一个 `tspan`，并给 `x`、`y`（y 是文字基线）；`text-anchor` 取 `start`、`middle`、`end` |
| `g` | 分组，可带 `transform`（translate、scale、rotate、matrix）；带 `id` 的 `g` 在 PowerPoint 里是一个组，可整体移动 |
| `image` | `href` 是工作区里的 png/jpg 相对路径，要写 `x`、`y`、`width`、`height` |
| `linearGradient`、`radialGradient` | 写在 `defs` 里，用 `fill="url(#名字)"`，2 到 10 个 `stop` |
| `filter` 加 `feDropShadow` | 给形状加阴影：`filter="url(#名字)"` |
| `marker` | 只当"箭头"用：`line`、`path` 上写 `marker-end="url(#名字)"`，线尾画三角箭头 |

可用的样式属性：`fill`、`stroke`、`stroke-width`、`stroke-dasharray`、`stroke-linecap`、`stroke-linejoin`、`opacity`、`fill-opacity`、`stroke-opacity`、`font-size`、`font-weight`、`font-family`、`font-style`、`text-decoration`、`letter-spacing`、`text-anchor`、`dominant-baseline`，写成属性或 `style="..."` 都行。

不能用（会被拒绝，并列出位置）：`style` 标签和 class、`use`、`symbol`、`clipPath`、`mask`、`pattern`、`foreignObject`、其他滤镜、动画、脚本、外链图片。要这些效果就用基本形状拼。

写法要点：

- **颜色用主题色名**：`primary`、`secondary`、`accent`、`surface`、`text`、`muted`、`line`、`soft`、`chart1` 到 `chart6`，和其他页配色一致，换 `theme` 也跟着变。也可以写 `#rrggbb`。
- 页面标题那一行的 `text` 写 `id="title"`（36 到 40 px、粗体，基线约 y=72，左边距 64）：它会成为这页的标题，大纲和导航里才看得到。
- 正文不小于 18 px，注释不小于 14 px；小于约 13.4 px（10 pt）`inspect_pptx` 会报告。
- 自己估文字宽度：汉字约一个字号宽，英文数字约 0.55 个字号。长句手动拆成多个 `tspan`，行距取字号的 1.4 到 1.6 倍；别让文字超出所在的框。
- 版面留白：左右边距 64，元素间距不小于 24，同一排的卡片等宽等高、对齐。一页形状不超过约 80 个。
- 装饰阴影、渐变要克制；一页只用一两种强调色。

一个流程页的写法（`svg` 字段的内容）：

```
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1280 720">
  <defs><marker id="arrow"><path d="M0 0 L8 4 L0 8 z"/></marker></defs>
  <rect width="1280" height="720" fill="surface"/>
  <text id="title" x="64" y="72" font-size="38" font-weight="bold" fill="primary">故障从发现到闭环只要四步</text>
  <g id="step1">
    <rect x="64" y="240" width="256" height="160" rx="14" fill="primary"/>
    <text x="192" y="310" font-size="28" font-weight="bold" fill="#FFFFFF" text-anchor="middle">1 发现</text>
    <text font-size="18" fill="soft" text-anchor="middle"><tspan x="192" y="352">告警自动派单</tspan></text>
  </g>
  <line x1="320" y1="320" x2="384" y2="320" stroke="muted" stroke-width="3" marker-end="url(#arrow)"/>
  <!-- 其余步骤同样的写法：x 依次加 320 -->
</svg>
```

被拒绝时，按返回的问题列表逐条修改，再把这一页的 `svg` 整页重发，不要只发改动的片段。生成后同样要用 `inspect_pptx` 检查。

## 套用公司模板

用户给了公司的 .pptx 或 .potx 模板（有 logo、标题栏、背景图）并要求"按这个模板做"时，用模板模式，不要自己画一套外观。母版、版式、logo、配色和字体原样沿用，内容页只替换标题和正文。

模板有两个来源：工作区里的 .pptx/.potx 文件，或编进程序的内置模板，写成 `builtin:名字`（公司标准模板叫 `builtin:company`）。用户说"用公司模板、标准模板"但没给文件时，先试 `builtin:company`；返回"none in this build"或"no built-in template"就是这个版本没带，再向用户要模板文件。两种来源的用法完全一样，下面的 `template` 都可以填 `builtin:company`。内置模板的页是空的，只用来读版式。

1. **先看模板有哪些版式**：`inspect_pptx` 传模板路径（或 `builtin:company`），`layouts: true`。返回的 `template` 里：
   - `layouts`：每个版式的编号 `id`、名称、占位符位置，`default_for` 标出默认的封面和内容页版式，`used_by_slides` 是模板自带样例页用了它；
   - `decor`：版式上的 logo、图片、色块。标 `keep clear` 的区域不要画东西；标 `full-slide` 的是整页背景图，内容页要避开它的主体；
   - `content_box_px`：内容页正文可用的区域，1280×720 的像素坐标。`svg` 页的图形都画在这个框里，标题不要画进 svg（标题由版式的标题占位符承载，位置和字号跟模板一致）；
   - `brand_color`：模板的品牌色。不写 `theme` 时配色自动取它。
2. **选版式**：一般不用选，默认封面 = 样例首页的版式，内容页 = 样例里最常用的版式。要换时写 `cover_layout`、`content_layout`、`closing_layout`，某一页单独换用 `template_layout`。值是版式编号（如 `"67"`）或名称；名称重名时会报错并列出编号，改用编号。
3. **生成**：`create_pptx` 加 `template`（工作区里的模板路径），其余字段照常。章节页用 `section`；标题长度、`svg` 压住模板装饰都会出现在 `warnings` 里，按提示改。
4. 模板模式下 `footer` 不起作用（页脚和页码由模板决定）；`theme` 不要写，写了就会盖掉模板品牌色；模板自带的样例页不会带进新稿。没有标题占位符的版式不能放 `bullets`、`table` 这类标准版式，换一个带标题的版式，或者用 `svg`。
5. 生成后同样用 `inspect_pptx` 检查，并把成品和模板放在一起核对：logo 没被遮、标题在原位。

模板里的版式只能沿用，不能新建；样例页上的图片、文字不会复制过去。

## 外观

- `theme`：`blue`（默认，深蓝加橙色点缀，稳重）、`mobile`（移动品牌蓝，图表配色更鲜艳）、`green`（绿色）、`red`（红金，适合表彰、党建类场合）、`gray`（低调的商务灰）。用户没要求就用默认；一份稿子只用一个主题。
- 字体默认微软雅黑，换字体用 `font`；对方电脑上可能没有的字体不要选。
- 每页底部的页脚默认是稿子标题，`footer` 为 `-` 表示不要。

## 生成之后：必须检查

1. 看返回里的 `outline`（每页标题）和 `warnings`。warning 基本都是"文字放不下"：精简文字、拆成两页，或换成表格，然后用 `overwrite: true` 重新生成。
2. 调用 `inspect_pptx`，读回每页内容并看 `issues`：
   - `warn`：没有标题、文字可能溢出、字号小于 10 pt、元素超出页面、两个元素重叠、空白页。必须处理。
   - `info`：图片或图表没有替代文字、单页文字过多。能改就改。
3. 修到没有 warn 再交付。交付时告诉用户页数、每页讲什么、用了哪些假设。

## 修改已有的 PPT

现在不能原位修改 .pptx。可以：

1. `inspect_pptx` 读出每页的文字、表格、图表数据和备注；
2. 按用户的修改意见整理成新的大纲；
3. 用 `create_pptx` 生成**新文件**（换个名字，别覆盖原稿）。

原稿的母版、动画、自定义版式和图片排版不会被带过去。用户的原稿是精心设计过的，就只给修改建议或文字稿，并说明原因。要保留原稿的外观，把它当模板用（见上一节），但样例页的内容需要重新写。

## 不要这样做

- 不要把整段文字塞进一页；不要每页都是 `bullets`，该用图表的数据就用图表。
- 标题不要用"概述""总结""其他"这类没有信息的词。
- 不要超过 60 页；饼图不要超过 6 个类别；表格不要超过 8 列。
- 不要在没有数据的情况下画图，也不要为了好看调整数据。
