package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/applog"
	"agentgo/internal/engine"
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if s.rt.runTrack != nil {
		s.rt.runTrack.Set(sessionID, "goroutine", "runStream 已启动")
	}
	if s.eventsOn() {
		ctx = agent.WithTraceEmitter(ctx, agent.TraceEmitterFromWails(func(p map[string]any) {
			p["session_id"] = sessionID
			if s.rt.runTrack != nil {
				comp, _ := p["component"].(string)
				name, _ := p["name"].(string)
				s.rt.runTrack.SetTrace(sessionID, comp, name)
			}
			s.emit("agent:trace", p)
		}))
		agent.EmitTrace(ctx, "start", "Runner", "stream", "流式任务已启动")
	}
	var res SendMessageResult
	emit := func(delta string) {
		if delta == "" {
			return
		}
		if s.rt.runTrack != nil {
			s.rt.runTrack.AddChunk(sessionID)
		}
		if s.eventsOn() {
			s.emit("chat:chunk", map[string]any{
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
	res = sendResultFromEngine(s.rt.runEngineTurn(ctx, engine.RunRequest{
		Engine: EinoEngineID, SessionID: sessionID, Input: userText, Images: images,
	}, engine.SinkFunc(func(ev engine.Event) {
		if ev.Type == engine.EventToken {
			delta, _ := ev.Payload["delta"].(string)
			emit(delta)
		}
	})))
	s.emitPendingMessages(sessionID, res.Messages)
	applog.Stream("done session=%s msgs=%d err=%q", sessionID, len(res.Messages), res.Error)
}

func (s *AppService) emitChatDone(sessionID string, res SendMessageResult) {
	if !s.eventsOn() {
		return
	}
	s.emit("chat:done", map[string]any{
		"session_id": sessionID,
		"messages":   res.Messages,
		"error":      res.Error,
		"degraded":   res.Degraded,
	})
}
