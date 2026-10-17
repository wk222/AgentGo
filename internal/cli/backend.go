// Package cli is AgentGo's own command-line client: one-shot runs, an
// interactive chat, and a few inspection commands. It drives the AgentGo brain
// directly (same engine router, governance, memory and plugins as the desktop
// app); it needs no Crush binary and speaks no Crush protocol.
//
// The package talks to the brain through Backend, so everything above it
// (rendering, the approval loop, the REPL, exit codes) is testable without a
// model.
package cli

import "agentgo/internal/engine"

// Message is one transcript entry returned by a decision or an answer.
type Message struct {
	Role       string
	Type       string // "text" | "approval" | "question" | ...
	Content    string
	ApprovalID string
	ToolName   string
	Arguments  string
	Status     string
}

// Pending reports whether the entry is a pause waiting for the user.
func (m Message) Pending() bool {
	return (m.Type == "approval" || m.Type == "question") && m.Status == "pending"
}

// Result is what resolving an approval or answering a question produced.
type Result struct {
	Messages []Message // what the resumed run said, including any further pause
	Error    string

	live bool // the resumed run's events were already delivered through a sink
}

// Streamer is implemented by backends that can deliver the events of the run a
// decision unblocks (the tool call, its diff, the reply as it is written).
// Without it a continued run shows up only as its final messages.
type Streamer interface {
	ApproveStream(approvalID string, approved bool, sink engine.EventSink) Result
	AnswerStream(questionID, text string, sink engine.EventSink) Result
}

// SessionInfo is one saved conversation.
type SessionInfo struct {
	ID        string
	Title     string
	UpdatedAt int64
	Messages  int
}

// PluginInfo is one plugin's state.
type PluginInfo struct {
	Name  string
	State string
	Error string
}

// Backend is the brain as the CLI sees it.
type Backend interface {
	// Run executes one turn and streams its events to sink. A turn that stops for
	// a decision returns Pending with the pause among Messages.
	Run(sessionID, input string, sink engine.EventSink) engine.RunResult
	// Approve records the decision on a paused tool call and, when approved,
	// continues the run.
	Approve(approvalID string, approved bool) Result
	// Answer replies to a question the agent asked and continues the run.
	Answer(questionID, text string) Result
	// Cancel stops the session's run in progress.
	Cancel(sessionID string)

	NewSession(title string) (string, error)
	Sessions() ([]SessionInfo, error)
	Plugins() []PluginInfo
	// Pending lists tool calls still waiting for a decision (left behind by runs
	// that ended with nobody to ask).
	Pending() []Message
	Tools() []string
	Close()
}

// Config says how to open the brain.
type Config struct {
	Workspace string // root the agent works in; empty = current directory
	API       string // "responses" streams reasoning summaries; empty = chat/completions
	Verbose   bool   // let the runtime's own log lines reach stderr (default: log file only)
}

// OpenFunc opens a Backend. Production code uses OpenStandalone.
type OpenFunc func(Config) (Backend, error)
