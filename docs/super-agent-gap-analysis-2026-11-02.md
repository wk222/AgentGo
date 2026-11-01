# 对照 DeepSeek Super-agent 架构：AgentGo 的差距与实测（2026-11-02）

参照文章：AgentsPulse 对 DeepSeek Harness 团队 *Super-agent Design Exploration* 的解读
（Router + Worker 两层、事件触发、线程订阅、异步工具、Watchdog、计算/执行分离）。
文章明确说那是架构探索，不是已发布的产品功能；原型仓库只是参考实现。

## 一句话

两边解决的不是同一个问题。Super-agent 解决的是"一个人同时盯不过来 3–5 个会话"，
靠常驻调度层把很多互相松散的任务并行推进；AgentGo 是本地优先、可审计的单人运行时，
强项在治理、审计、回退、工作区与文档类任务，**缺的是"多任务并行调度"这一整层**。

## 逐项对照

| Super-agent 的设计 | AgentGo 现状 | 差距 |
| --- | --- | --- |
| 常驻 Router，只对话+派活，Worker 隔离执行 | IM 每个聊天一个固定会话（`im:<adapter>:<chat>`）；Matrix 有协调者会话，但不是通用路由 | 缺：没有"所有入口汇到一个调度会话，再派给隔离 Worker" |
| 事件触发（IM、邮件、CI、评审评论）+ 两级过滤（程序白名单 → LLM 判定） | 有 5 类触发（进程退出/文件/日志匹配/webhook/定时）、cron、`watch_event`；IM 只有程序化 `allow_from` | 缺：邮件/CI/PR 事件源；缺 LLM 慢路径过滤 |
| 线程订阅：同一 PR/话题的后续事件直达已有 Worker，跨渠道识别同一件事 | 订阅 = 已布置的触发器，按会话 ID 唤醒 | 缺：按 PR/话题/邮件线程做订阅与去重 |
| 异步 `start()/stop()`：立刻返回 Started，结果作为新事件回来 | 有 `job_monitor`、`task_output/task_stop`、`taskhub`；但 `execute_bash` 是同步阻塞，且多个 shell 调用被串行 | 部分：能力在，但默认 shell 不走它；`job.completed` 目前没人订阅去自动开一轮 |
| 子智能体隔离与并行 | `invoke_subagent`、`run_swarm`（并行 goroutine）、深度上限 | 有；缺全局并发上限与"谁来决定委派" |
| Watchdog：独立会话+定时消息，处理 API 失败与卡住/跑偏的 Worker | 有启动恢复、LLM 流重试/空闲超时；心跳默认关且没有处理方 | 缺：运行级的卡死/跑偏巡检与自动续跑 |
| 按渠道塑形回复（IM ≤100 字，PR/邮件结构化） | IM 按平台上限分片；无 PR/邮件模板 | 缺：渠道级回复策略 |
| 上下文当缓存：Inline & Spill + ID 逐跳回溯 | 有 spill（预览+`fetch_spill_content`）、压缩、记忆召回带 ID、上下文事件日志 | 基本有；缺统一的"回溯链"抽象 |
| 计算/状态/执行分离，BYOM 轻量守护进程 | 浏览器版、沙箱（本机/landlock/docker）；执行与 Agent 循环同进程 | 缺：云端循环 + 用户机器守护进程 |
| 投机执行：静默复现并修复，确认了才开口 | 有定时/Matrix 跟进，但都是特定事件、默认关 | 缺：通用的"不被问也先干、没把握就不吭声" |

AgentGo 有而文章里没有的：逐次工具审批与审计、回退到模型视图的破甲流程、逆向证据账本、真实 Office 文档工具、工作流编辑器、内置小应用沙箱。

## 实测（qwen3.7-max，每次核对请求体里的 `model` 字段）

任务：工作区里有一个要跑 20 秒的脚本，让模型对 A、B、C 各跑一次，互相独立，
汇总到 `summary.txt`。提示里不提并行。串行约 60 秒，并行约 20 秒。

| 档位 | 结果 |
| --- | --- |
| 标准档 | 通过，总和正确，46 秒。模型自己写了一个 Python 脚本用 `subprocess` 同时起三个，脚本只用 20.5 秒；**没有**用 `invoke_subagent`/`run_swarm` |
| reverse 档（修复后） | 通过，总和正确，75 秒。模型在同一条回复里发了 3 个 `execute_bash`，但执行是**串行**的（3×20 秒） |

两点读数：

- 文章里"中性提示下模型从不调用 Subagent 工具"的现象，AgentGo 上同样存在；它靠自己写脚本并行，而不是用委派工具。
- 同一条回复里的多个 shell 调用被当作"会改东西"的调用串行执行；没有"先返回已启动，稍后再收结果"的 shell。这正是文章异步 `start()/stop()` 想解决的点。

## 实测里挖出的 3 个真 bug（已修）

1. **多个工具同时等审批时，checkpoint 保存失败**（`gob: type not registered for interface: map[string]string`）。
   整轮降级成无工具闲聊，模型还会谎称"我不能运行脚本"。v1.2.0 就有。
   修复：在 `internal/governance/tool_approval_state.go` 注册 `map[string]string` 与 `[]string`，并加回归测试。
2. **`execute_bash` 自带一道 6 月遗留的中断，和治理层叠在一起。** 策略已经放行（reverse / red_team 自动批准）时，
   它仍然停住，且中断 ID 不在审批队列里，`approve` 永远报 "not found or already resolved"，
   这些档位下 shell 命令根本跑不起来。v1.2.0 就有。
   修复：删掉工具自带的中断，审批只由治理层决定（标准档仍然每次询问）。
3. （踩坑记录，不是代码 bug）我的终端残留了 `AGENTGO_MODE_PROFILE=reverse`，前几次"标准档"实际跑在 reverse 档。
   跑对照实验前先清环境变量，并用请求转储核对模型名和档位。

## 已落地的提升

1. **`execute_bash` 支持 `run_in_background`**：立刻返回任务 ID，模型用已有的 `task_output`（可阻塞等待）取结果，`task_stop` 终止。
   一条回复里发多条互相独立的命令就能同时跑。
   - 根因之一：`NewBackgroundTaskCoordinator` 建了执行器注册表却没交给任务管理器，任何本地后台任务都会报
     `executor "eino.dev/process-local" is unavailable`。已修。
   - 没有后台能力的运行时（或关闭 EINO 后台任务）里，这个参数被忽略，命令照常前台跑。
   - 实测（reverse 档，同样的 3×20 秒任务）：shell 部分 60 秒 → 约 20 秒，总耗时 75 秒 → 33–54 秒；模型在不被提示的情况下自己选了后台模式。
2. **承诺未兑现续跑**（`AGENTGO_PROMISE_NUDGE`，默认开）：用过工具的回合，如果最后一句只是"现在将结果写入 …"这类动作承诺、
   没调用工具，就追加一次续跑让它真的去做。规则很窄：只触发一次、不碰问句/已完成/对用户的建议。
   实测 3 次里 2 次被救回（文件落盘），第 3 次是模型自称"没有写文件的工具"，不属于这一类。

## 值得借鉴、按性价比排序（第 1 项已完成一半：后台启动已有，`job.completed` 自动开一轮还没接）

1. **让 shell 支持后台启动**（`start` 返回句柄，完成后作为事件回到会话）。基础件都在（`job_monitor`、`taskhub`），缺的是接线：给 `job.completed` 配一个订阅者自动开一轮，并让同一条回复里的独立 shell 调用可以并行。
2. **运行级 Watchdog**：把现有心跳接上处理方，检查"卡住/连续同一报错"，自动续跑或升级给人。
3. **线程订阅**：IM 会话/PR/邮件线程各给一个稳定 ID，后续事件直接投递给已有会话。
4. **触发器补齐**：GitHub PR/CI、邮件两类事件源，加一个 LLM 慢路径过滤。
5. **Router + Worker** 放最后：它要的是前面几项都就绪，而且收益只在"多任务并行"场景；单人本地使用时不如前四项划算。
