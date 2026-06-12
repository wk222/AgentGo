package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/applog"
)

// SendMessageStream runs the agent in a goroutine and emits Wails events:
// chat:chunk {session_id, delta}, chat:done {session_id, messages, error}, approval:pending
func (s *AppService) SendMessageStream(sessionID, userText string, images []string) map[string]any {
	applog.IPC("SendMessageStream", "enter session=%s len=%d", sessionID, len(userText))
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		applog.Warn("SendMessageStream empty session_id — events will not be scoped")
	}
	applog.Stream("start session=%s len=%d", sessionID, len(userText))
	if s.rt.runTrack != nil {
		s.rt.runTrack.Begin(sessionID, "SendMessageStream")
	}
	go s.runStream(sessionID, userText, images)
	return map[string]any{"success": true, "streaming": true, "session_id": sessionID}
}

func (s *AppService) runStream(sessionID, userText string, images []string) {
	if sessionID != "" {
		pctx := context.Background()
		var meta map[string]any
		if len(images) > 0 {
			meta = map[string]any{"images": images}
		}
		_ = s.rt.Sessions().AppendMessage(pctx, sessionID, "user", userText, "text", meta)
		_ = s.rt.Sessions().AutoTitleFromUserMessage(pctx, sessionID, userText)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if s.rt.runTrack != nil {
		s.rt.runTrack.Set(sessionID, "goroutine", "runStream 已启动")
	}
	if s.app != nil {
		ctx = agent.WithTraceEmitter(ctx, agent.TraceEmitterFromWails(func(p map[string]any) {
			p["session_id"] = sessionID
			if s.rt.runTrack != nil {
				comp, _ := p["component"].(string)
				name, _ := p["name"].(string)
				s.rt.runTrack.SetTrace(sessionID, comp, name)
			}
			s.app.Event.Emit("agent:trace", p)
		}))
		agent.EmitTrace(ctx, "start", "Runner", "stream", "流式任务已启动")
	}
	var streamed strings.Builder
	var res SendMessageResult
	emit := func(delta string) {
		if delta == "" {
			return
		}
		if s.rt.runTrack != nil {
			s.rt.runTrack.AddChunk(sessionID)
		}
		streamed.WriteString(delta)
		if s.app != nil {
			s.app.Event.Emit("chat:chunk", map[string]any{
				"session_id": sessionID,
				"delta":      delta,
			})
		}
	}

	defer func() {
		if r := recover(); r != nil {
			applog.Warn("runStream panic session=%s: %v", sessionID, r)
			res = SendMessageResult{Error: fmt.Sprintf("Agent 异常: %v", r)}
		}
		phase, errMsg := "done", res.Error
		if errMsg != "" {
			phase = "error"
		}
		if s.rt.runTrack != nil {
			s.rt.runTrack.Finish(sessionID, phase, errMsg)
		}
		s.emitChatDone(sessionID, res)
	}()

	if s.rt.runTrack != nil {
		s.rt.runTrack.Set(sessionID, "agent_run", "sendMessageCore")
	}
	res = s.sendMessageCore(ctx, sessionID, userText, images, emit)
	fullStream := strings.TrimSpace(streamed.String())
	if fullStream != "" {
		merged := false
		for i := range res.Messages {
			m := &res.Messages[i]
			if m.Role == "assistant" && (m.Type == "" || m.Type == "text") {
				if len(fullStream) > len(strings.TrimSpace(m.Content)) {
					m.Content = fullStream
					m.Type = "text"
				}
				merged = true
				break
			}
		}
		if !merged && res.Error == "" {
			res.Messages = append(res.Messages, ChatMessageDTO{
				Role: "assistant", Type: "text", Content: fullStream,
			})
		}
	}
	applog.Stream("done session=%s msgs=%d streamed=%d err=%q", sessionID, len(res.Messages), len(fullStream), res.Error)

	if sessionID != "" {
		persistCtx := context.Background()
		persistTypes := map[string]int{}
		for _, m := range res.Messages {
			t := m.Type
			if t == "" {
				t = "text"
			}
			persistTypes[t]++
			meta := map[string]any{}
			if m.Type == "approval" {
				meta["approval_id"] = m.ApprovalID
				meta["tool_name"] = m.ToolName
				meta["arguments"] = m.Arguments
				meta["status"] = m.Status
			}
			_ = s.rt.Sessions().AppendMessage(persistCtx, sessionID, m.Role, m.Content, m.Type, meta)
		}
		applog.Stream("persist session=%s types=%s", sessionID, applog.FormatCounts(persistTypes))
	}
}

func (s *AppService) emitChatDone(sessionID string, res SendMessageResult) {
	if s.app == nil {
		return
	}
	s.app.Event.Emit("chat:done", map[string]any{
		"session_id": sessionID,
		"messages":   res.Messages,
		"error":      res.Error,
	})
}
