package bridge

import (
	"context"
	"strings"
	"time"

	"agentgo/internal/agent"
	"agentgo/internal/applog"
	"agentgo/internal/engine"
)

// EngineEventName is the Wails event carrying engine.Event payloads.
const EngineEventName = "engine:event"

const engineRunTimeout = 15 * time.Minute

// Engines exposes the unified engine router (register more engines here).
func (s *AppService) Engines() *engine.Router { return s.engines }

// ListEngines returns registered engine ids.
func (s *AppService) ListEngines() []string { return s.engines.Engines() }

// ListActiveEngineRuns returns in-flight runs.
func (s *AppService) ListActiveEngineRuns() []engine.ActiveRun { return s.engines.Active() }

// CancelEngineRun cancels one run by id.
func (s *AppService) CancelEngineRun(runID string) map[string]any {
	return map[string]any{"success": s.engines.Cancel(strings.TrimSpace(runID))}
}

// CancelEngineSession cancels every active run of a session.
func (s *AppService) CancelEngineSession(sessionID string) map[string]any {
	n := s.engines.CancelSession(strings.TrimSpace(sessionID))
	return map[string]any{"success": true, "cancelled": n}
}

// wailsSink forwards engine events to the frontend (no-op when headless).
func (s *AppService) wailsSink() engine.EventSink {
	return engine.SinkFunc(func(ev engine.Event) {
		// Resumed segments retain their run identity, but start a new emitter.
		// Give desktop subscribers one monotonic sequence across all segments.
		ev.Seq = s.desktopEventSeq.Add(1)
		if s.app != nil {
			s.app.Event.Emit(EngineEventName, ev)
		}
	})
}

// RunEngine starts a run asynchronously. Progress is emitted on "engine:event"
// (status/token/tool_call/.../done). engineID may be empty for the default.
func (s *AppService) RunEngine(engineID, sessionID, input string, images []string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	runID := s.engines.NewRunID()
	if s.rt.runTrack != nil {
		s.rt.runTrack.Begin(sessionID, "RunEngine")
	}
	go func() {
		res := s.runEngineTurn(context.Background(), engine.RunRequest{
			RunID: runID, Engine: strings.TrimSpace(engineID),
			SessionID: sessionID, Input: input, Images: images,
		}, s.wailsSink())
		if s.rt.runTrack != nil {
			phase := "done"
			if res.Error != "" {
				phase = "error"
			}
			s.rt.runTrack.Finish(sessionID, phase, res.Error)
		}
	}()
	return map[string]any{"success": true, "run_id": runID, "session_id": sessionID}
}

// RunEngineSync runs to completion and returns the result (headless/MCP use).
func (s *AppService) RunEngineSync(engineID, sessionID, input string, images []string, sink engine.EventSink) engine.RunResult {
	return s.runEngineTurn(context.Background(), engine.RunRequest{
		Engine: strings.TrimSpace(engineID), SessionID: strings.TrimSpace(sessionID),
		Input: input, Images: images,
	}, sink)
}

// runEngineTurn persists the user turn, runs via the router, then persists the
// final messages (mirrors what the legacy runStream path does).
func (s *AppService) runEngineTurn(parent context.Context, req engine.RunRequest, sink engine.EventSink) engine.RunResult {
	ctx, cancel := context.WithTimeout(parent, engineRunTimeout)
	defer cancel()

	if req.SessionID != "" {
		ctx = agent.WithHistory(ctx, s.sessionHistory(ctx, req.SessionID))
		var meta map[string]any
		if len(req.Images) > 0 {
			meta = map[string]any{"images": req.Images}
		}
		_ = s.rt.Sessions().AppendMessage(ctx, req.SessionID, "user", req.Input, "text", meta)
		_ = s.rt.Sessions().AutoTitleFromUserMessage(ctx, req.SessionID, req.Input)
	}

	// Publish completion only after the transcript and commit point are stored.
	// A client can safely reload the session as soon as it receives done.
	var completion []engine.Event
	forward := engine.SinkFunc(func(ev engine.Event) {
		if ev.Type == engine.EventDone || ev.Type == engine.EventApprovalRequest {
			completion = append(completion, ev)
			return
		}
		if sink != nil {
			sink.Emit(ev)
		}
	})
	res := s.engines.Run(ctx, req, s.logRunEvents(req.SessionID, forward))

	if req.SessionID != "" {
		for _, m := range res.Messages {
			meta := map[string]any{}
			if m.Type == "approval" || m.Type == "question" {
				meta["approval_id"] = m.ApprovalID
				meta["tool_name"] = m.ToolName
				meta["arguments"] = m.Arguments
				meta["status"] = m.Status
			}
			_ = s.rt.Sessions().AppendMessage(context.Background(), req.SessionID, m.Role, m.Content, m.Type, meta)
		}
		// Commit point: only now is the transcript complete for this run.
		s.commitRun(req.SessionID, res.RunID, res)
	}
	if sink != nil {
		for _, ev := range completion {
			sink.Emit(ev)
		}
	}
	applog.Stream("engine run=%s engine=%s session=%s msgs=%d err=%q",
		res.RunID, res.Engine, req.SessionID, len(res.Messages), res.Error)
	return res
}
