// Package engine defines AgentGo's unified agent-runtime contracts.
//
// An AgentEngine runs one request (a chat turn, a coding task, ...) and reports
// progress to an EventSink. Frontends (Wails GUI, MCP, headless HTTP) consume the
// same Event stream regardless of which engine produced it.
//
// This package must not import internal/bridge (bridge adapts engines to it).
package engine

import (
	"context"
	"time"
)

// EventType is the kind of a streamed Event.
type EventType string

const (
	EventStatus          EventType = "status"           // lifecycle: started / done / cancelled / error
	EventToken           EventType = "token"            // assistant text delta
	EventReasoning       EventType = "reasoning"        // reasoning/thinking delta
	EventToolCall        EventType = "tool_call"        // tool invocation started
	EventToolResult      EventType = "tool_result"      // tool invocation finished
	EventFileChange      EventType = "file_change"      // file mutated: path/before/after/unified_diff
	EventApprovalRequest EventType = "approval_request" // human approval needed
	EventError           EventType = "error"
	EventDone            EventType = "done" // terminal event, exactly one per run
)

// Event is the unified progress record.
type Event struct {
	RunID     string         `json:"run_id"`
	SessionID string         `json:"session_id,omitempty"`
	Engine    string         `json:"engine"`
	Seq       int64          `json:"seq"`
	Type      EventType      `json:"type"`
	Payload   map[string]any `json:"payload,omitempty"`
	At        time.Time      `json:"at"`
}

// EventSink receives events. Implementations must be safe for concurrent use
// and must not block for long.
type EventSink interface {
	Emit(Event)
}

// SinkFunc adapts a function to EventSink.
type SinkFunc func(Event)

func (f SinkFunc) Emit(e Event) { f(e) }

// MultiSink fans an event out to several sinks (nil sinks are skipped).
func MultiSink(sinks ...EventSink) EventSink {
	return SinkFunc(func(e Event) {
		for _, s := range sinks {
			if s != nil {
				s.Emit(e)
			}
		}
	})
}

// DiscardSink drops all events.
var DiscardSink EventSink = SinkFunc(func(Event) {})

// RunRequest describes one unit of work.
type RunRequest struct {
	RunID     string // assigned by Router when empty
	Engine    string // engine id; Router default when empty
	SessionID string
	Input     string
	Images    []string
	Workspace string         // optional cwd override
	Meta      map[string]any // engine-specific options
}

// Message is a final, persisted-style output message.
type Message struct {
	Role       string `json:"role"`
	Type       string `json:"type"` // text | approval | question
	Content    string `json:"content,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	Arguments  string `json:"arguments,omitempty"`
	Status     string `json:"status,omitempty"`
}

// RunResult is the final outcome of a run.
type RunResult struct {
	RunID    string    `json:"run_id"`
	Engine   string    `json:"engine"`
	Messages []Message `json:"messages,omitempty"`
	Error    string    `json:"error,omitempty"`
	// Pending is true when the run stopped waiting for approval/answer.
	Pending bool `json:"pending,omitempty"`
}

// AgentEngine executes RunRequests. Cancellation is via ctx; Router owns it.
// An engine reports progress with sink.Emit using the Emitter helper, and
// returns the final result. Engines must NOT emit EventDone themselves.
type AgentEngine interface {
	ID() string
	Run(ctx context.Context, req RunRequest, sink Emitter) (RunResult, error)
}

// Emitter is the engine-facing view of a sink bound to one run; it fills in
// run id, session, engine, seq and timestamp.
type Emitter interface {
	Status(state string, extra map[string]any)
	Token(delta string)
	Reasoning(delta string)
	ToolCall(id, name, args string)
	ToolResult(id, name, output string, isError bool)
	FileChange(path, before, after, unifiedDiff string)
	ApprovalRequest(approvalID, toolName, arguments, prompt string)
	Error(msg string)
}
