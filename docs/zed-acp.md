# 用 Zed 作为 AgentGo 前端（ACP）

## 接入方式

Zed 设置 `agent_servers`（已对照 Zed 源码 `docs/src/ai/external-agents.md`）：

```json
{
  "agent_servers": {
    "AgentGo": {
      "type": "custom",
      "command": "C:\\path\\to\\agentgo.exe",
      "args": ["--acp"],
      "env": {}
    }
  }
}
```

AgentGo 在 `--acp` 模式下走 stdin/stdout 的换行分隔 JSON-RPC 2.0，不启动 Wails 窗口。

## 本次对照 Zed 做的修正

| 问题 | 之前 | 现在 |
|---|---|---|
| 流式更新的方法名 | `sessionUpdate` | `session/update`（ACP 标准，Zed 只认这个） |
| 思考块类型 | `thought` | `agent_thought_chunk` |
| `session/cancel` 是通知 | 误回一个响应 | 不回应 |
| 客户端发来的响应（无 method） | 被当请求回 `Method not found` | 直接忽略 |
| `tool_call` 字段位置 | 嵌套在 `toolCall` | 平铺在 update 上（ACP 标准） |

## 验证情况

- 用 `agent-client-protocol-schema 0.14`（Zed 使用 3.3.0 SDK 的同源 schema）的 serde 定义逐字段核对了 `initialize` / `session/new` / `session/prompt` 响应与 `session/update` 通知的字段名和必填项，形状匹配。
- `internal/acp/zed_compat_test.go` 按 Zed 的真实方法名回放一遍握手 + 提示 + 取消，设置 `ACP_DUMP=<path>` 可导出原始输出。
- **没有**真正启动过 Zed：本机无 MSVC `link.exe`，C 盘仅剩 2GB，无法编译 Zed；Zed 官方 Windows 安装包也没装。需要用户装 Zed 后实测。

## 未完成（接 Zed 前必须补）

1. **审批**：AgentGo 的 `code_write` 等会产生 pending 审批并结束本轮，靠 `ResolveApproval` 续跑。ACP 下需要：
   - Server 支持向客户端发请求并等待响应（现在只有单向通知）；
   - 在 `HandlePrompt` 发现 pending 审批时调用 `session/request_permission`，拿到结果后 `ResolveApproval` 并继续流式输出。
   没做之前，Zed 里触发写文件会直接停住。
2. **工具调用可视化**：目前只流式文本，没有发 `tool_call` / `tool_call_update`。
3. **文件/终端**：ACP 允许走 `fs/read_text_file`、`fs/write_text_file`、`terminal/*` 让 Zed 持有未保存缓冲区；AgentGo 现在直接读写磁盘，Zed 里有未保存修改时可能冲突。
4. `session/new` 默认返回的模型列表（deepseek / claude）是写死的，应改成读真实配置。
5. A2UI 卡片在 ACP 里没有对应物，只能降级为文本。

## 下一步

先补第 1 项（双向请求 + request_permission），再装 Zed 做首次真机联调。
