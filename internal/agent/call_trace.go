package agent

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agentgo/internal/config"
	"agentgo/internal/redact"
)

// A call trace follows one tool call from what the model wrote to what came back, so a wrong answer can be
// traced to the step that went wrong: the arguments exactly as the model sent them, the arguments after
// repair, the policy decision, the result, and — at run level — context compaction and token usage.
//
// It is off unless AGENTGO_CALL_TRACE is set or the debug view switches it on. It lives in memory only
// (a few recent sessions), every text is redacted for credentials and clipped before it is kept.
const (
	callTraceMaxSessions = 8
	callTraceMaxEvents   = 400
	callTraceMaxRunes    = 2000
)

// Stages of a trace, in the order a call goes through them.
const (
	TraceStageModelArgs    = "model_args"    // arguments exactly as the model sent them
	TraceStageArgsRepaired = "args_repaired" // only when repair changed them
	TraceStagePolicy       = "policy"        // allow / deny / ask / approved / rejected / executed
	TraceStageResult       = "result"        // what the tool returned (or the error), with duration
	TraceStageCompaction   = "compaction"    // run level: context before/after a summary
	TraceStageUsage        = "usage"         // run level: tokens of one model call
)

// CallTraceEvent is one step. CallID is empty for run-level steps (compaction, usage).
type CallTraceEvent struct {
	Seq        int64          `json:"seq"`
	SessionID  string         `json:"session_id"`
	RunID      string         `json:"run_id"`
	CallID     string         `json:"call_id,omitempty"`
	Tool       string         `json:"tool,omitempty"`
	Stage      string         `json:"stage"`
	At         int64          `json:"at"`
	DurationMS int64          `json:"duration_ms,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

type callTraceStore struct {
	mu       sync.Mutex
	sessions map[string][]CallTraceEvent
	order    []string // oldest session first
}

var (
	callTrace    = &callTraceStore{sessions: map[string][]CallTraceEvent{}}
	callTraceSeq atomic.Int64
	// 0 = follow the environment flag, 1 = on, 2 = off (set by the debug view).
	callTraceMode atomic.Int32
	runIDSeq      atomic.Uint64
)

// CallTraceEnabled reports whether calls are being traced.
func CallTraceEnabled() bool {
	switch callTraceMode.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	return config.CallTrace.On()
}

// SetCallTraceEnabled switches tracing for this process. Turning it off keeps what was already recorded.
func SetCallTraceEnabled(on bool) {
	if on {
		callTraceMode.Store(1)
	} else {
		callTraceMode.Store(2)
	}
}

type runIDKey struct{}

// RunIDFromContext is the id of the run this context belongs to ("" outside a run).
func RunIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(runIDKey{}).(string)
	return s
}

func newRunID() string {
	return "run_" + strconv.FormatInt(time.Now().UnixMilli(), 36) + "_" + strconv.FormatUint(runIDSeq.Add(1), 36)
}

// traceSession is the id the desktop knows a session by (the run may carry an "agentic:" prefix).
func traceSession(id string) string { return strings.TrimPrefix(strings.TrimSpace(id), "agentic:") }

type traceCallKey struct{}

func withTraceCallID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, traceCallKey{}, id)
}

func traceCallIDFrom(ctx context.Context) string {
	s, _ := ctx.Value(traceCallKey{}).(string)
	return s
}

// traceText hides credentials and keeps the text small.
func traceText(s string) string {
	s = redact.Secrets(s)
	r := []rune(s)
	if len(r) <= callTraceMaxRunes {
		return s
	}
	return string(r[:callTraceMaxRunes]) + "…(+" + strconv.Itoa(len(r)-callTraceMaxRunes) + " chars)"
}

// recordCallTrace keeps one step. A no-op when tracing is off; data values that are strings are redacted and clipped.
func recordCallTrace(ctx context.Context, callID, tool, stage string, took time.Duration, data map[string]any) {
	if !CallTraceEnabled() {
		return
	}
	if callID == "" {
		callID = traceCallIDFrom(ctx)
	}
	clean := make(map[string]any, len(data))
	for k, v := range data {
		if s, ok := v.(string); ok {
			v = traceText(s)
		}
		clean[k] = v
	}
	ev := CallTraceEvent{
		Seq: callTraceSeq.Add(1), SessionID: traceSession(SessionIDFromContext(ctx)), RunID: RunIDFromContext(ctx),
		CallID: callID, Tool: tool, Stage: stage, At: time.Now().UnixMilli(), DurationMS: took.Milliseconds(), Data: clean,
	}
	callTrace.add(ev)
}

func (s *callTraceStore) add(ev CallTraceEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, known := s.sessions[ev.SessionID]
	if !known {
		s.order = append(s.order, ev.SessionID)
		for len(s.order) > callTraceMaxSessions {
			delete(s.sessions, s.order[0])
			s.order = s.order[1:]
		}
	}
	list = append(list, ev)
	if len(list) > callTraceMaxEvents {
		list = append([]CallTraceEvent(nil), list[len(list)-callTraceMaxEvents:]...)
	}
	s.sessions[ev.SessionID] = list
}

// CallTraceFilter narrows a trace; empty fields match everything.
type CallTraceFilter struct {
	RunID  string
	CallID string
}

// CallTraceFor returns the recorded steps of a session, oldest first. Run-level steps (compaction, usage) of a run
// are kept when a call filter is given and the run matches, so one call can be read next to its context.
func CallTraceFor(sessionID string, f CallTraceFilter) []CallTraceEvent {
	sessionID = traceSession(sessionID)
	callTrace.mu.Lock()
	list := append([]CallTraceEvent(nil), callTrace.sessions[sessionID]...)
	callTrace.mu.Unlock()
	if f.RunID == "" && f.CallID == "" {
		return list
	}
	runs := map[string]bool{}
	if f.CallID != "" {
		for _, e := range list {
			if e.CallID == f.CallID {
				runs[e.RunID] = true
			}
		}
	}
	out := list[:0:0]
	for _, e := range list {
		switch {
		case f.RunID != "" && e.RunID != f.RunID:
		case f.CallID != "" && e.CallID != f.CallID && !(e.CallID == "" && runs[e.RunID]):
		default:
			out = append(out, e)
		}
	}
	return out
}

// CallTraceSessions lists the sessions that have steps, newest first.
func CallTraceSessions() []string {
	callTrace.mu.Lock()
	defer callTrace.mu.Unlock()
	out := make([]string, 0, len(callTrace.order))
	for i := len(callTrace.order) - 1; i >= 0; i-- {
		out = append(out, callTrace.order[i])
	}
	return out
}

// ClearCallTrace forgets a session's steps ("" forgets everything).
func ClearCallTrace(sessionID string) {
	sessionID = traceSession(sessionID)
	callTrace.mu.Lock()
	defer callTrace.mu.Unlock()
	if strings.TrimSpace(sessionID) == "" {
		callTrace.sessions = map[string][]CallTraceEvent{}
		callTrace.order = nil
		return
	}
	delete(callTrace.sessions, sessionID)
	for i, id := range callTrace.order {
		if id == sessionID {
			callTrace.order = append(callTrace.order[:i], callTrace.order[i+1:]...)
			break
		}
	}
}
