> 后续实现见 [摘要树与上下文预算](summary-tree-budget-2026-10-24.md)。本文件记录初版状态；初版累计读取额度已被后续版本的当前上下文占用预算取代，且已新增递归摘要树。

# 分层记忆与预算：2026-10-24

本轮落地的是可运行的渐进检索基础，不是完整复现 MemForest，也不是“所有长期存储永不增长”。

## 研究取舍

- [Concyclics/MemForest](https://github.com/Concyclics/MemForest)：README 描述 canonical facts、session/entity/scene 时间树、局部维护、先召回树再展开证据。借鉴时间区间与渐进展开，保留 AgentGo 的 SQLite 权威记录、来源修订与 Eino Session。
- [Celina-love-sweet/MemForest](https://github.com/Celina-love-sweet/MemForest)：本轮读取 README 和 Mem0/evaluation/MemForest.py。代码有语义分组、MST 选对、逐对摘要及重嵌入、按 keep-ratio 减少节点、Qdrant 删除和更新。README 提醒会覆盖原始记忆。这个离线压缩流程不能直接当桌面应用的无损容量管理器。
- 不照搬固定保留比例或覆盖原记录；不同版本的论文日期、横向性能与模型适配效果，本轮未独立复现实验验证。

## 已实现

1. **预算可配置**：记忆面板 → 概览 → 记忆上下文预算。总量默认 4000，自动注入默认 1000，范围 0–32768，自动量不能超过总量。保存至独立 memory-settings.json，原模型配置不改动；启动时显式环境变量优先。
   - AGENTGO_MEMORY_TOKENS
   - AGENTGO_MEMORY_AUTO_TOKENS
   - AGENTGO_MEMORY_HIERARCHY
2. **主题目录**：主题 → 类型 → 原记录。新原子记忆支持短 topic；旧记录按 modality 回退，不强制全库模型重写。目录是权威表的实时 SQL 投影，归档、更正后随之变化。
3. **时间目录**：browse_memory 的 timeline：年份 → 月份 → 原记录。UTC、左闭右开区间，依据 created_at（记录时间，不假装是事件发生时间）。
4. **主动读取**：同一会话与 global，支持 browse、expand、timeline、search、read。查询不依赖主题路由；搜索沿用现有 FTS/向量召回，在有向量服务时使用混合召回。since 为可选 Unix 时间下限。
5. **共享运行预算**：主 ADK 路径自动注入与并发主动展开共用计数，JSON 正文与来源信息一起计费；每页最多 8 项，原文每页最多 4000 Unicode 字符，预算不足时进一步缩短并返回 next_offset。额度限制读取，不停止记忆写入。
6. **跨轮清理**：下一次运行或来源修订变化时，将旧 browse_memory 结果投影成刷新提示，并记录原生 Session 替换事件。保持 tool call/result 配对，不修改归档原消息对象，不让旧来源继续常驻模型上下文。
7. **去重**：相同 scope + 完整输入生成 journal 使用稳定内容 ID，重复输入命中已存在记录时不再调用模型；相同蒸馏输出复用父 ID。已归档/被替代/被否定的同 ID 记录不会因顺序重复处理而复活。内容不同不做不可靠的强行合并。
8. **另一条 AgenticMessage 路径**：记忆注入改为独立带标记消息，每轮替换而非追加到基础 system；刷新为空时移除旧注入。

## 限制与后续

- Token 是统一估算值，不是各模型精确 tokenizer；框架控制提示、工具声明、预算耗尽提示不计作记忆正文。整体模型输入另有 Session Spine 上限。
- 默认最多 300 Token 用于任务相关目录行；完整目录按页加载，不将全库目录常驻。
- 磁盘证据允许增长。本轮没有新增“活跃事实容量到阈值后后台语义合并”的容量控制器；重复去重与有上限的读取不能冒充该能力。
- 当前目录是主题/类型及时间投影，不是实体关系树或学习出的语义 EventTree。大库目录聚合成本随库量增加，尚无百万级规模基准。
- 原始证据留在现有记录/Session 体系，不新增第二套权威存储；归档记录可凭 ID 在同 scope 读取。搜索和目录主要展示有效记录。
- 主 ADK schema.Message 路径有 browse_memory；AgenticMessage 路径本轮只修复重复注入，尚未接同一主动展开工具。
- 后续容量整理应建立显式成员关系与来源修订依赖，在事务中提交新索引，再切换活跃视图；不删除证据，不将摘要作为新事实。不以简单的“每满 N 条就合并一半”替代质量验证。

## 验证

新增覆盖：主题目录分页与更正、会话隔离、时间边界、中文预算与原文分页、并发共享额度、来源修订使旧结果失效、归档不复活、重复输入不增长、配置持久化、真实 Eino ADK 动态工具调用、AgenticMessage 重复注入。

构建与回归结果在交付时报告。没有宣称真实内网模型/UI 手工验证通过。

## 本轮交付验证

- internal/promptbudget、sessions、core、arch、governance、config 全量测试通过；internal/memory 全量测试在修复 hook-only Pipeline 兼容性后通过。
- internal/agent、internal/bridge 的上下文、记忆、Session、审批/工具调用、流超时相关选择性回归通过（未声称整个仓库所有网络测试通过）。
- 前端 typecheck、14 项测试与生产构建通过；保留既有 Monaco 大包提示。
- 真实 Eino ADK 测试模型验证 browse_memory 声明经 model.Option 传入，并实际执行返回证据，不是只测手动调用。
- 产物：bin/agentgo-hierarchical-memory.exe，62607360 字节，Windows amd64，GUI subsystem=2。
- SHA256：5C78290D02A4E7732591DCD6B13F9AF33D99637C814520B198A5A4F97910299B
- EXE 包含此前尚未打包的流读取隔离、审批绑定、工具执行错误传播等修改，以及本轮分层记忆工作。未启动 GUI、未用真实内网模型完成手工验收。
- 此前阻塞证据为 exec-server transport disconnected / recovery timed out、Windows 环境不可用并切到空 Linux 工作目录。本轮 Windows 路径与读写均正常，没有复现连接断开；编译/测试失败与连接故障分别处理。
