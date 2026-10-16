package bridge

import (
	"context"
	"strings"

	"agentgo/internal/core"
	"agentgo/internal/engine"
)

// The run log itself (events, commit points) lives in core; this file only
// binds it to the session store of this process and exposes it over IPC.

// logRunEvents persists the durable events of one session's run before passing
// every event on unchanged.
func (s *AppService) logRunEvents(sessionID string, next engine.EventSink) engine.EventSink {
	return core.LogRunEvents(s.rt.Sessions(), sessionID, next)
}

// commitRun writes the run's commit point. Call it after the final messages
// have been stored: a log that ends in Done means the transcript is complete.
func (s *AppService) commitRun(sessionID, runID string, res engine.RunResult) {
	s.rt.commitRunWith(sessionID, runID, res, nil)
}

func (s *AppService) commitRunWith(sessionID, runID string, res engine.RunResult, extra map[string]any) {
	s.rt.commitRunWith(sessionID, runID, res, extra)
}

// commitRunWith is commitRun plus extra facts in the commit payload.
func (rt *Runtime) commitRunWith(sessionID, runID string, res engine.RunResult, extra map[string]any) {
	core.CommitRun(rt.Sessions(), sessionID, runID, res, extra)
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
