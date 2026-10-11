package engine

import (
	"sync"
	"sync/atomic"
	"time"
)

// emitter is the per-run event source. It enforces the protocol-v1 stream rules:
//
//   - Seq is monotonic within a run.
//   - Exactly one EventDone ends a run; nothing is emitted after it.
//   - After EventInterrupted the run may still land in-flight tool results and
//     errors, but token/audio_chunk are dropped (nothing may keep speaking).
type emitter struct {
	sink      EventSink
	runID     string
	sessionID string
	engine    string
	seq       atomic.Int64

	mu          sync.Mutex // serializes emit so Seq order == delivery order
	finished    bool
	interrupted bool
	reason      string
}

var (
	_ Emitter      = (*emitter)(nil)
	_ BlockEmitter = (*emitter)(nil)
)

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

// emit delivers one event unless the stream rules say it must be dropped.
//
// The lock covers only the state decision and the Seq assignment; the sink is
// called outside it so a sink may call back into the Router (e.g. Interrupt)
// without deadlocking.
func (e *emitter) emit(t EventType, p map[string]any) {
	e.mu.Lock()
	if e.finished || (e.interrupted && (t == EventToken || t == EventAudioChunk)) {
		e.mu.Unlock()
		return
	}
	switch t {
	case EventDone:
		e.finished = true
	case EventInterrupted:
		if e.interrupted {
			e.mu.Unlock()
			return // one interrupted per run
		}
		e.interrupted = true
		e.reason, _ = p["reason"].(string)
	}
	ev := Event{
		RunID: e.runID, SessionID: e.sessionID, Engine: e.engine,
		Seq: e.seq.Add(1), Type: t, Payload: p, At: time.Now(),
	}
	e.mu.Unlock()
	e.sink.Emit(ev)
}

// interrupt records and announces an interruption; reports whether this call did.
func (e *emitter) interrupt(reason string) bool {
	if reason == "" {
		reason = "user"
	}
	e.mu.Lock()
	already := e.interrupted || e.finished
	e.mu.Unlock()
	if already {
		return false
	}
	e.emit(EventInterrupted, map[string]any{"reason": reason})
	return true
}

func (e *emitter) wasInterrupted() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.interrupted
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

// ToolResult normalizes an empty output once, for every engine: an empty tool
// message is dropped downstream and the next model request fails with HTTP 400.
func (e *emitter) ToolResult(id, name, output string, isError bool) {
	p := map[string]any{"id": id, "name": name, "is_error": isError}
	out, empty := NormalizeToolOutput(output)
	p["output"] = out
	if empty {
		p["empty"] = true
	}
	e.emit(EventToolResult, p)
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

func (e *emitter) Block(t EventType, b ContentBlock) {
	if err := b.Validate(); err != nil {
		e.Error("invalid " + string(t) + " block: " + err.Error())
		return
	}
	e.emit(t, map[string]any{"block": b})
}

func (e *emitter) InputTranscript(text string, partial bool) {
	if text == "" {
		return
	}
	e.emit(EventInputTranscript, map[string]any{"text": text, "partial": partial})
}

func (e *emitter) done(res RunResult) {
	p := map[string]any{
		"messages": res.Messages, "error": res.Error, "pending": res.Pending,
	}
	if res.Reason != "" {
		p["reason"] = res.Reason
	}
	e.emit(EventDone, p)
}
