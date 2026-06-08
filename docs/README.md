# AgentGo 内部文档索引

> Markdown 默认 **不入 Git**（见仓库根 `.gitignore`）。克隆后在本机维护 `docs/`。

## 唯一实现 SSOT（接线与 Eino 原语）

**[eino-capabilities.md](./eino-capabilities.md)** — 什么已用 Eino、什么禁止自研、环境变量、收口主路径。

## 差距文档（只写「还缺什么」）

| 文件 | 用途 |
|------|------|
| [pybot-gap.md](./pybot-gap.md) | 与 PyBot **产品能力面** 对照（无完成度百分比） |
| [pybot-paper-gap.md](./pybot-paper-gap.md) | 与 PyBot **论文** 对照（无完成度百分比） |
| [openclaw-gap.md](./openclaw-gap.md) | 与 OpenClaw 对照 |

**不要**在差距文档里重复「已实现清单」或写互相矛盾的完成度；实现状态以 `eino-capabilities.md` + 代码为准。

## 专题

| 文件 | 用途 |
|------|------|
| [eino-chapters.md](./eino-chapters.md) | 官方文档章节索引 |
| [memory-eino.md](./memory-eino.md) | 记忆 × Eino |
| [product-features.md](./product-features.md) | 桌面产品能力（面向用户/测试） |
| [build-reproducible.md](./build-reproducible.md) | 可复现构建与本地 replace |

## 历史（只读参考）

`eino-architecture-plan.md`、`development-plan.md`、`pybot-gap.md` 旧百分比已废弃。
