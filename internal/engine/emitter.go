package engine

import (
	"sync/atomic"
	"time"
)

type emitter struct {
	sink      EventSink
	runID     string
	sessionID string
	engine    string
	seq       atomic.Int64
}

func newEmitter(sink EventSink, runID, sessionID, engine string) *emitter {
	return &emitter{sink: sink, runID: runID, sessionID: sessionID, engine: engine}
}

// ResumeEmitter binds a checkpoint continuation to its original run. The caller
// must call finish once, after persisting the continuation's final transcript.
func ResumeEmitter(sink EventSink, runID, sessionID, engineID string) (Emitter, func(RunResult)) {
	if sink == nil {
		sink = DiscardSink
	}
	e := newEmitter(sink, runID, sessionID, engineID)
	return e, e.done
}

func (e *emitter) emit(t EventType, p map[string]any) {
	e.sink.Emit(Event{
		RunID: e.runID, SessionID: e.sessionID, Engine: e.engine,
		Seq: e.seq.Add(1), Type: t, Payload: p, At: time.Now(),
	})
}

func (e *emitter) Status(state string, extra map[string]any) {
	p := map[string]any{"state": state}
	for k, v := range extra {
		p[k] = v
	}
	e.emit(EventStatus, p)
}

func (e *emitter) Token(delta string) {
	if delta != "" {
		e.emit(EventToken, map[string]any{"delta": delta})
	}
}

func (e *emitter) Reasoning(delta string) {
	if delta != "" {
		e.emit(EventReasoning, map[string]any{"delta": delta})
	}
}

func (e *emitter) ToolCall(id, name, args string) {
	e.emit(EventToolCall, map[string]any{"id": id, "name": name, "arguments": args})
}

func (e *emitter) ToolResult(id, name, output string, isError bool) {
	e.emit(EventToolResult, map[string]any{"id": id, "name": name, "output": output, "is_error": isError})
}

func (e *emitter) FileChange(path, before, after, unifiedDiff string) {
	e.emit(EventFileChange, map[string]any{
		"path": path, "before": before, "after": after, "unified_diff": unifiedDiff,
	})
}

func (e *emitter) ApprovalRequest(approvalID, toolName, arguments, prompt string) {
	e.emit(EventApprovalRequest, map[string]any{
		"approval_id": approvalID, "tool_name": toolName, "arguments": arguments, "prompt": prompt,
	})
}

func (e *emitter) Error(msg string) { e.emit(EventError, map[string]any{"message": msg}) }

func (e *emitter) done(res RunResult) {
	e.emit(EventDone, map[string]any{
		"messages": res.Messages, "error": res.Error, "pending": res.Pending,
	})
}
