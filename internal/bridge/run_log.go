package bridge

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"agentgo/internal/engine"
	"agentgo/internal/sessions"
)

// The run log is the durable record of what a run did, kept next to the chat
// transcript. It stores facts (tool calls and results, file changes, approval
// requests, errors) and one commit point per run. Streaming deltas are
// transient and are not stored.

const (
	runLogPayloadMax = 16 << 10 // bytes kept per string field; big outputs are cut, not dropped
	runLogCutMark    = "…[truncated]"
)

// cutString keeps at most max bytes without splitting a UTF-8 sequence.
func cutString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + runLogCutMark
}

// durableRunEvent maps an engine event to a log entry. ok is false for events
// that are not stored. Done is deliberately not mapped: the commit point is
// written explicitly, after the final messages are persisted.
func durableRunEvent(e engine.Event) (ev sessions.RunEvent, ok bool) {
	p := map[string]any{}
	str := func(k string) string { s, _ := e.Payload[k].(string); return s }
	switch e.Type {
	case engine.EventStatus:
		ev.Kind = sessions.KindStatus
		p["state"] = str("state")
	case engine.EventToolCall:
		ev.Kind, ev.CallID, ev.Name = sessions.KindToolCall, str("id"), str("name")
		p["arguments"] = cutString(str("arguments"), runLogPayloadMax)
	case engine.EventToolResult:
		ev.Kind, ev.CallID, ev.Name = sessions.KindToolResult, str("id"), str("name")
		p["output"] = cutString(str("output"), runLogPayloadMax)
		p["is_error"], _ = e.Payload["is_error"].(bool)
	case engine.EventFileChange:
		// before/after can be whole files; the diff is the fact worth keeping.
		ev.Kind, ev.Name = sessions.KindFileChange, str("path")
		p["unified_diff"] = cutString(str("unified_diff"), runLogPayloadMax)
	case engine.EventApprovalRequest:
		ev.Kind, ev.CallID, ev.Name = sessions.KindApproval, str("approval_id"), str("tool_name")
		p["arguments"] = cutString(str("arguments"), runLogPayloadMax)
	case engine.EventError:
		ev.Kind = sessions.KindError
		p["message"] = cutString(str("message"), runLogPayloadMax)
	default:
		return sessions.RunEvent{}, false
	}
	b, err := json.Marshal(p)
	if err != nil {
		return sessions.RunEvent{}, false
	}
	ev.PayloadJSON = string(b)
	ev.RunID, ev.SessionID = e.RunID, e.SessionID
	return ev, ev.SessionID != "" && ev.RunID != ""
}

// logRunEvents persists the durable events of one session's run before passing
// every event on unchanged.
func (s *AppService) logRunEvents(sessionID string, next engine.EventSink) engine.EventSink {
	if next == nil {
		next = engine.DiscardSink
	}
	st := s.rt.Sessions()
	if sessionID == "" || st == nil {
		return next
	}
	return engine.SinkFunc(func(e engine.Event) {
		if ev, ok := durableRunEvent(e); ok {
			if _, err := st.AppendRunEvent(context.Background(), ev); err != nil {
				log.Printf("[runlog] session=%s run=%s %s: %v", sessionID, e.RunID, ev.Kind, err)
			}
		}
		next.Emit(e)
	})
}

// commitRun writes the run's commit point. Call it after the final messages
// have been stored: a log that ends in Done means the transcript is complete.
func (s *AppService) commitRun(sessionID, runID string, res engine.RunResult) {
	s.rt.commitRunWith(sessionID, runID, res, nil)
}

func (s *AppService) commitRunWith(sessionID, runID string, res engine.RunResult, extra map[string]any) {
	s.rt.commitRunWith(sessionID, runID, res, extra)
}

// commitRunWith is commitRun plus extra facts in the commit payload (for
// example how an approval was resolved). A run may be committed more than once:
// it commits as pending when it pauses, and again when it is resumed or
// refused; the latest commit decides its state.
func (rt *Runtime) commitRunWith(sessionID, runID string, res engine.RunResult, extra map[string]any) {
	st := rt.Sessions()
	if sessionID == "" || st == nil || runID == "" {
		return
	}
	payload := map[string]any{
		"error":    cutString(res.Error, runLogPayloadMax),
		"pending":  res.Pending,
		"messages": len(res.Messages),
	}
	for k, v := range extra {
		payload[k] = v
	}
	b, _ := json.Marshal(payload)
	if _, err := st.AppendRunEvent(context.Background(), sessions.RunEvent{
		SessionID: sessionID, RunID: runID, Kind: sessions.KindDone, PayloadJSON: string(b),
	}); err != nil {
		log.Printf("[runlog] session=%s run=%s commit: %v", sessionID, runID, err)
	}
}

// GetRunEvents returns the run log of a session after afterSeq, for clients
// that reconnect or open the same session from another surface.
func (s *AppService) GetRunEvents(sessionID string, afterSeq int64, limit int) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return map[string]any{"success": false, "error": "session_id is required"}
	}
	events, err := s.rt.Sessions().ListRunEvents(context.Background(), sessionID, afterSeq, limit)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	state, err := s.rt.Sessions().LastRunState(context.Background(), sessionID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "session_id": sessionID, "events": events, "run": state}
}
