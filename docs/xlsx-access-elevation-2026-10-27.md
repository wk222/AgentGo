# Excel 分析/修改、多会话、工作区外文件、管理员权限（2026-10-27）

本轮回答四个问题：能不能同时开多个会话；能不能（经用户允许）访问工作区以外的文件；能不能按需请求管理员权限；"分析 Excel 做通报 / 修改表格"这个技能是否真的好用。每一项都用真实模型（真实环境 `ds`）在网页版/CLI 里实测过，下面只写结论、做法、限制。

## 1. 分析 Excel 做通报 / 修改表格

**测试材料**：一张故意做得不干净的省级月报（16 个地市 × 9 个月，覆盖率有的存成小数 0.9581、有的是文本，有空格、合计行、空白格、标题两行），外加"考核目标"表。真值由脚本直接从源表算出（`truth.json`），与模型产出逐项对账。

**发现并修掉的问题**

| 现象 | 根因 | 处理 |
| --- | --- | --- |
| 模型写公式把覆盖率乘了两次 100、或拿日期列和文本 "2026-09" 比较，反复试公式耗尽步数（"已达到本轮的步数上限"） | 模型看不到列"真实怎么存"的：百分比是小数还是百分点、日期是真日期还是文本 | `analyze_xlsx` 的 profile 新增 `stored` 字段，直接写明存法和该用什么公式写法；技能里补了"按真实存储写公式"配方 |
| 对已被其他软件写过的单元格加格式时报 `fill pattern value must be between 0 and 18` | excelize `GetStyle→NewStyle` 往返会产生 Pattern=-1 | `sanitizeStyle`，附回归测试 |
| 条件格式（标红）在 WPS 里不生效 | 规则公式带了前导 `=` | 存储前去掉 `=` |
| 文本型数字无法参与计算 | 没有就地清洗手段 | 新增 `edit_xlsx` 操作 `to_number`、`copy_format`；`save_as` 加 `overwrite`（默认不覆盖） |
| 先读完整张表再算，浪费上下文 | `read_xlsx` 默认读太多 | 默认 40 行，截断提示直接指向 `analyze_xlsx` |

**验证方式（不依赖模型自述）**：修改类任务让 WPS 通过 COM 重算并导出（`wps_dump.ps1`），再用 `check_xlsx.py` 对照源表真值检查新增列的每个值和红色填充的位置；并用 WPS 渲染成 PDF/PNG 看实际外观。结果：新增列数值与真值逐格一致、标红行集合与真值一致，一次运行 34 秒、无步数上限错误。

**通报类任务回归**（本轮最后又跑了一遍）：同一份脏数据让模型写《9月网络指标通报》docx，全省 5G 平均 96.72、4G 平均 99.06、投诉 4490、故障 16、新增站点 201、前 2/后 3 名、未达标地市数 7、与 8 月变化最大地市、投诉前 3，**要求里问到的数字全部与真值一致**；源表自带的合计行没有被重复计入，并在"数据口径说明"里写明简单平均而非加权。

## 2. 同时开多个会话

可以，而且是实测过的：网页版里同时 `NewSession` 三个会话并同时 `RunEngine`，三个运行的时间窗口互相重叠（0.5s 起，分别 61.7s / 73.9s / 37.0s 结束），每个会话的事件只带自己的 `session_id`（外来事件 0 个），三个答案各自正确、没有串内容。机制：取消/插话（steer）按会话分开管理，事件按会话打标，前端按 `session_id` 分流。

限制：多个会话共用同一个工作区时，改同一个文件会互相覆盖，没有文件锁；真要并行改文件，请让它们改不同的文件。

## 3. 访问工作区以外的文件（用户允许时）

**做法**：模型直接传绝对路径（`E:\共享\季度.xlsx`）。策略层（`PathPolicyStage`）发现路径在工作区外，就把这次调用挂起等批准，审批卡上写明"访问工作区以外的路径：…；同意后，本次运行期间可读写这些位置所在的文件夹：…"。批准后该文件夹（含子文件夹）在本次运行期间对该工作区放行，同一文件夹里的其他文件不再询问，别的文件夹仍要问；拒绝则什么都不放行。授权只存在内存里，重启即失效。

**实测**（网页版 + 真模型）：
- 批准：分析工作区外的 xlsx → 弹一次审批 → 同文件夹另存为新文件（写操作本身另有一次审批）→ 读回验证，全程共 2 次审批，结果正确。
- 拒绝：审批后没有读取、没有授权。
- 系统目录：读 `C:\Windows\win.ini` 直接被拒（"不可授权（系统目录）"），不弹审批。
- 打包版复验（2026-10-28，`0.1.0-win7.10` 网页版，真模型）：工作区外的文本文件和 xlsx 各读一次，都是弹 1 次审批、批准后读到正确内容；同一文件夹里再读一次不再审批；拒绝则没有读到；`@完整路径` 提及的文本文件不审批、直接读到。详见 `docs/p0-quick-wins-2026-10-28.md` 第 6 节。

**永远不放行**：`..` 穿越、盘符根目录、系统目录（Windows、Program Files 等）、凭据目录（`.ssh`、`.aws`、`.gnupg`、`.kube`、`.docker`、gcloud、Credentials）、AgentGo 自己的数据目录（里面有 API Key）；`strict` 模式下完全不允许出工作区。

**实现位置**：`internal/pathgrant`（登记表）、`internal/governance/policy_pipeline.go`（`PathPolicyStage`、`GrantOutsideDirs`）、`internal/governance/invoke_policy.go`（批准后授权）、`internal/tools/workspace_code.go`（各工具的路径校验认授权）。审批卡现在会显示策略给出的原因（`internal/bridge/approval_resume.go`）。

限制：只覆盖走统一路径校验的工具（办公文档工具、工作区文件工具、代码工具）；`execute_bash`/`run_batch_script` 本来就能碰任何路径，靠它们自己的逐次审批把关。

## 4. 按需请求管理员权限（Windows）

**工具**：`run_elevated`（仅 Windows 注册，其他系统模型看不到）。一次只提权**一条命令**，不会常驻提权、不会改 UAC 设置。

**安全设计**
- 每次调用都要用户批准，**不能记住、不会被"自动批准"会话或 `open` 模式放行**（单测覆盖）。审批卡写明命令、原因，并提示可能弹 UAC。
- 命令以 base64 放在启动参数里，不落文件，避免提示窗口出现期间被同用户的别的程序改掉。
- 拒绝清单：`diskpart`、`bcdedit`、`format X:`、`vssadmin/wbadmin delete`、`cipher /w`、新建用户/加管理员组、`reg delete HK…`、`sc delete`、关 Defender/防火墙、删整盘等，用户需要时自己手动做。
- 用户在 UAC 里点"否"→ 明确回报"命令没有运行"；超时（默认 120 秒，上限 10 分钟）→ 如实说明提权进程可能还在跑（非管理员进程无法结束它）。
- "自动请求"的含义：普通命令因权限失败（拒绝访问/需要提升）时，`execute_bash` 的结果会附一行提示，模型据此决定是否发起 `run_elevated`；工具说明要求"不要第一次就用、不要用来绕过拒绝"。**不会静默提权。**

**实测**
- 单测：脚本构造（含中文、引号、`'` 的路径）、输出/退出码/中文 stderr 捕获、拒绝清单、"永不豁免审批"。
- 真实提权（`AGENTGO_TEST_UAC=1 go test ./internal/tools -run RunElevatedThroughUAC`）：`net session` 在提权后返回成功。这台开发机的 UAC 策略是"管理员不提示直接提升"（`ConsentPromptBehaviorAdmin=0`），所以**没有 UAC 弹窗，AgentGo 的审批卡就是唯一一道关**；策略为"提示"的电脑会多一次 UAC。
- 真模型端到端：普通权限运行 `net session` 得到"拒绝访问"→ 模型发起 `run_elevated` 并写出原因 → 审批卡显示原因 → 批准后命令以管理员身份成功。

限制：Win7 上需要自带 PowerShell 2.0（系统自带）；如果 AgentGo 以无人值守方式运行（服务/远程会话），UAC 无人可点会超时；标准用户（非管理员）会被要求输入管理员密码，运行身份是那个管理员账号。

## 5. 其他

- 本轮发现仓库根目录有两个 0 字节的 `xlsx_analyze.go`/`xlsx_table.go`（工具误建的垃圾文件，会让 `go vet ./...` 失败），已删除。
- 新包 `internal/pathgrant` 已登记到 `internal/arch/layers.go`（Foundation 层，仅标准库）。
- 技能 `office` 增加了"文件不在工作区时直接用绝对路径"的说明；`office-xlsx`、`office-report` 的改动见上文第 1 节。

## 复现

```text
go test ./internal/officedoc ./internal/pathgrant ./internal/governance ./internal/tools
go test -tags web ./cmd/... ./internal/bridge ./internal/hostui
AGENTGO_TEST_UAC=1 go test ./internal/tools -run RunElevatedThroughUAC -v   # 真实提权，会触发 UAC 策略
```
