package bridge

import (
	"strings"

	"agentgo/internal/agent"
)

// 请求级调试串联：一次工具调用从“模型原样写的参数”到“结果回给模型”的每一步，以及上下文压缩和 token 用量。
// 默认关闭（环境变量 AGENTGO_CALL_TRACE 或这里打开）；只在内存里留最近几个会话，文本已隐藏密钥并截断。

// GetCallTrace 返回一个会话记录下来的步骤（旧→新）。runID/callID 非空时只返回对应那一次运行/调用，
// 并附上同一次运行的上下文压缩与用量。sessionID 为空时只返回开关状态和有记录的会话。
func (s *AppService) GetCallTrace(sessionID, runID, callID string) map[string]any {
	out := map[string]any{"success": true, "enabled": agent.CallTraceEnabled(), "sessions": agent.CallTraceSessions()}
	if sid := strings.TrimSpace(sessionID); sid != "" {
		events := agent.CallTraceFor(sid, agent.CallTraceFilter{RunID: strings.TrimSpace(runID), CallID: strings.TrimSpace(callID)})
		if events == nil {
			events = []agent.CallTraceEvent{}
		}
		out["events"] = events
	}
	return out
}

// SetCallTrace 打开或关闭调用追踪（只对之后的调用生效，已记录的保留）。
func (s *AppService) SetCallTrace(enabled bool) map[string]any {
	agent.SetCallTraceEnabled(enabled)
	return map[string]any{"success": true, "enabled": agent.CallTraceEnabled()}
}

// ClearCallTrace 清掉一个会话的记录；sessionID 为空则清掉全部。
func (s *AppService) ClearCallTrace(sessionID string) map[string]any {
	agent.ClearCallTrace(sessionID)
	return map[string]any{"success": true}
}
