# agentgo-doctor

用户问"为什么没有某个工具、某个功能用不了、怎么开启或关闭某项能力、AgentGo 的报错看不懂、某个限制在哪里调"时先读本技能：按证据一层层排查，不要凭印象猜，也不要让用户去改你没读到的配置。

## 排查顺序（从最常见的原因往后）

1. **读工具返回的原话。** 错误文字里通常已经写了原因（"no workspace selected""not found on PATH""requires approval"）。先引用原文，再解释。
2. **工具是不是被收起来了。** 不常用的工具默认不在工具列表里，要先用 `tool_search` 搜出来再调用；接入的 MCP 工具、动态工具大多是这样。搜不到再往下查。
3. **开关是不是关了。** 很多能力由插件和 `AGENTGO_*` 开关控制，见下表。开关是环境变量，改完要**重启 AgentGo** 才生效。
4. **协作方式拦住了。** 设置 → 协作方式分严格、平衡（默认）、开放：严格模式下改文件和运行命令都要先问，且不允许新建工具、委派子智能体、访问工作区以外的文件夹；开放模式只免去在工作区里改文件的逐次确认，运行命令、申请管理员权限、新建工具、访问工作区以外的文件夹仍要问。被拒绝就说明原因、等用户决定，不要换个路径绕过去。
5. **缺外部程序。** Python、Git、Docker、`pwsh`、`node` 没装或不在 `PATH` 里。告诉用户装什么，不要自己下载安装。
6. **没选工作区。** 多数文件类工具以工作区为根，让用户先在界面里选好工作区文件夹。
7. **模型或网络。** 让用户在设置里对模型服务点"测试连接"。网关不返回用量时，用量里的 Token 显示为 0 不是故障。

## 功能开关

| 开关 | 默认 | 管什么 |
| --- | --- | --- |
| `AGENTGO_OFFICEDOC` | 开 | Word、Excel、PPT 工具，以及 `office` 开头的内置技能 |
| `AGENTGO_DOC_INDEX` | 开 | 文档索引工具（`index_documents`、`search_documents` 等）和 `document-qa` 技能 |
| `AGENTGO_BROWSER_CONTROL` | 开 | 驱动 Chrome 或 Edge 的浏览器工具；找不到浏览器就不会出现；每个动作都要批准 |
| `AGENTGO_EXTERNAL_AGENT` | 开 | `external_agent`：把任务交给本机的 Claude Code、Codex、Gemini CLI 等 |
| `AGENTGO_COMPUTER_USE` | 关 | `computer_use`：让模型看屏幕、控制鼠标键盘（仅 Windows，Ctrl+Alt+Esc 随时停止） |
| `AGENTGO_OCR` | 关 | `ocr_image`：读出图片里的文字 |
| `AGENTGO_REKIT` | 开 | 逆向工具包 |
| `AGENTGO_USER_HOOKS` | 开 | 数据目录里 `hooks.json` 定义的用户钩子；设为 0 全部停用 |
| `AGENTGO_CODETOOLS_APPROVE` | 开 | 设为 0 时，所有会改文件的代码工具一律被拒绝（只读） |
| `AGENTGO_PLUGINS_DISABLE` | 空 | 逗号分隔的插件名，关掉这些可选插件（核心插件关不掉） |
| `AGENTGO_DISABLE_SCHEDULER` | 关 | 设为 1 时不启动定时器，`schedule_task` 的任务不会触发 |
| `AGENTGO_SANDBOX_OS` | 关 | 用操作系统沙箱限制 `execute_bash`；开着时 `run_skill_script` 不能用 |

开关写 `0` 或 `1`（也认 `true`、`false`、`on`、`off`、`yes`、`no`）；写了认不出的值，等于没设，按默认。

## 常见限制在哪里调

| 现象 | 开关 | 默认 |
| --- | --- | --- |
| 命令跑到一半被结束 | `AGENTGO_SHELL_TIMEOUT_SEC` | 180 秒；模型可以申请更长，最多 1800 秒；0 表示没有默认上限 |
| 命令吃光内存或进程太多（Windows） | `AGENTGO_SHELL_MEM_MB`、`AGENTGO_SHELL_MAX_PROCS` | 物理内存的 70%；256 个进程 |
| 代码搜索超时、结果被截断 | `AGENTGO_GREP_TIMEOUT_SEC`、`AGENTGO_GLOB_TIMEOUT_SEC`、`AGENTGO_SEARCH_LIMIT` | 5 秒、30 秒、每页 100 条 |
| 看文件一次读得太少 | `AGENTGO_VIEW_LINES` | 200 行 |
| 外部智能体一轮跑太久 | `AGENTGO_EXTERNAL_AGENT_MINUTES` | 20 分钟 |
| 不想读上层目录的 `AGENTS.md` | `AGENTGO_AGENTSMD_PARENTS` | 开 |
| 技能没有出现 | `AGENTGO_SKILL_DIRS`、`AGENTGO_EXTERNAL_SKILLS` | 空；技能怎么放见 `skill-writer` |

任务本来就要跑很久时，先别调大上限：用 `job_monitor` 放到后台跑，或用 `watch_event` 等结果出来再被叫醒。

## 按症状走

- **某个工具找不到**：先 `tool_search`；再看上面的开关；再看是不是协作方式或只读状态把它收起来了；浏览器、屏幕控制、图片识别这几类还取决于本机有没有浏览器、有没有打开对应开关。
- **技能没出现在列表里**：技能列表只显示前面一部分，直接用 `activate_skill` 按名字加载；文件必须叫 `SKILL.md`，放的位置见 `skill-writer`。
- **MCP 工具没出现或连不上**：按 `mcp-connect` 排查。
- **文档搜不到或内容过期**：按 `document-qa` 排查：文件改过要刷新索引，扫描件没有文字层读不出来。
- **定时任务没触发**：`schedule_task` 的 `action` 为 `list` 看任务是否存在、是否暂停；到点时 AgentGo 需要在运行；再查 `AGENTGO_DISABLE_SCHEDULER`。
- **一直被问批准**：这是协作方式在起作用，不是故障；想少问就请用户到设置里改协作方式，不要替他改。

## 收集材料给维护人员

设置 → 用量与诊断 → "生成诊断包"：里面有版本、系统、日志末尾和运行参数，密钥和用户名已隐藏，不含对话内容、记忆内容和工作区文件。数据目录（`agentgo.log` 日志、数据库和设置文件都在那里）的位置也在同一页。桌面版打不开时，常见原因是缺少 WebView2 运行时，可以改用浏览器版 `AgentGo-web.exe`。

## 纪律

- 密钥和令牌不读、不复述、不让用户发给你。
- 改环境变量、安装软件、改系统设置这类事，先说明影响和做法，由用户决定和动手；除非用户明确要求，不要用 `execute_bash` 去做。
- 说"原因是……"之前要有证据（工具的原话、文件内容、开关的实际值）；没有证据就说"可能是……，请确认……"。
- 说"已经好了"之前要重试一次并看到成功。
