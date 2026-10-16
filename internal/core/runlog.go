package core

import (
	"context"
	"encoding/json"
	"log"

	"agentgo/internal/engine"
	"agentgo/internal/sessions"
)

// The run log is the durable record of what a run did, kept next to the chat
// transcript. It stores facts (tool calls and results, file changes, approval
// requests, errors) and one commit point per run. Streaming deltas are
// transient and are not stored.

const (
	RunLogPayloadMax = 16 << 10 // bytes kept per string field; big outputs are cut, not dropped
	RunLogCutMark    = "…[truncated]"
)

// CutString keeps at most max bytes without splitting a UTF-8 sequence.
func CutString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + RunLogCutMark
}

// DurableRunEvent maps an engine event to a log entry. ok is false for events
// that are not stored. Done is deliberately not mapped: the commit point is
// written explicitly, after the final messages are persisted.
func DurableRunEvent(e engine.Event) (ev sessions.RunEvent, ok bool) {
	p := map[string]any{}
	str := func(k string) string { s, _ := e.Payload[k].(string); return s }
	switch e.Type {
	case engine.EventStatus:
		ev.Kind = sessions.KindStatus
		p["state"] = str("state")
	case engine.EventToolCall:
		ev.Kind, ev.CallID, ev.Name = sessions.KindToolCall, str("id"), str("name")
		p["arguments"] = CutString(str("arguments"), RunLogPayloadMax)
	case engine.EventToolResult:
		ev.Kind, ev.CallID, ev.Name = sessions.KindToolResult, str("id"), str("name")
		p["output"] = CutString(str("output"), RunLogPayloadMax)
		p["is_error"], _ = e.Payload["is_error"].(bool)
	case engine.EventFileChange:
		// before/after can be whole files; the diff is the fact worth keeping.
		ev.Kind, ev.Name = sessions.KindFileChange, str("path")
		p["unified_diff"] = CutString(str("unified_diff"), RunLogPayloadMax)
	case engine.EventApprovalRequest:
		ev.Kind, ev.CallID, ev.Name = sessions.KindApproval, str("approval_id"), str("tool_name")
		p["arguments"] = CutString(str("arguments"), RunLogPayloadMax)
	case engine.EventError:
		ev.Kind = sessions.KindError
		p["message"] = CutString(str("message"), RunLogPayloadMax)
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

// LogRunEvents persists the durable events of one session's run to st before
// passing every event on unchanged. A nil store or empty session passes through.
func LogRunEvents(st *sessions.Store, sessionID string, next engine.EventSink) engine.EventSink {
	if next == nil {
		next = engine.DiscardSink
	}
	if sessionID == "" || st == nil {
		return next
	}
	return engine.SinkFunc(func(e engine.Event) {
		if ev, ok := DurableRunEvent(e); ok {
			if _, err := st.AppendRunEvent(context.Background(), ev); err != nil {
				log.Printf("[runlog] session=%s run=%s %s: %v", sessionID, e.RunID, ev.Kind, err)
			}
		}
		next.Emit(e)
	})
}

// CommitRun writes the run's commit point, plus extra facts in the payload (for
// example how an approval was resolved). A run may be committed more than once:
// it commits as pending when it pauses, and again when it is resumed or
// refused; the latest commit decides its state. Call it after the final
// messages have been stored: a log that ends in Done means the transcript is
// complete.
func CommitRun(st *sessions.Store, sessionID, runID string, res engine.RunResult, extra map[string]any) {
	if sessionID == "" || st == nil || runID == "" {
		return
	}
	payload := map[string]any{
		"error":    CutString(res.Error, RunLogPayloadMax),
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

// AwaitingRunID returns the run the log shows as paused for this pending run's
// session, or "" when there is none (matrix/workflow runs, sessions without a
// log, or paths that never wrote one). Only a run that is awaiting approval can
// be closed.
func AwaitingRunID(ctx context.Context, st *sessions.Store, pr PendingRun) string {
	if pr.ResumeKind != "" || pr.SessionID == "" || st == nil {
		return ""
	}
	state, err := st.LastRunState(ctx, pr.SessionID)
	if err != nil || state.Status != sessions.RunAwaitingApproval {
		return ""
	}
	return state.RunID
}

// AppendRunStatus adds a status fact to an existing run.
func AppendRunStatus(st *sessions.Store, sessionID, runID, state string) {
	if runID == "" || st == nil {
		return
	}
	if _, err := st.AppendRunEvent(context.Background(), sessions.RunEvent{
		SessionID: sessionID, RunID: runID, Kind: sessions.KindStatus,
		PayloadJSON: `{"state":"` + state + `"}`,
	}); err != nil {
		log.Printf("[runlog] session=%s run=%s status: %v", sessionID, runID, err)
	}
}
