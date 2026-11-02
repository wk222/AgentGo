package coderuntime

import (
	"context"
	"time"
)

// ToolResult is what one tool call hands back to the agent loop.
type ToolResult struct {
	Content string
	IsError bool
}

// Tool is one callable code tool, independent of any agent framework. Schema
// holds the whole JSON Schema of the arguments, with "type":"object" on top.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Parallel    bool
	// Run performs the call; argsJSON carries the arguments the model produced.
	Run func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error)
}

// LSPClientInfo describes one language server for the desktop panel. State
// follows the numbering of codelsp.ServerState.
type LSPClientInfo struct {
	Name            string    `json:"name"`
	State           int       `json:"state"`
	Error           string    `json:"error,omitempty"`
	DiagnosticCount int       `json:"diagnostic_count"`
	ConnectedAt     time.Time `json:"connected_at"`
}

// LSPEvent tells a listener that a server moved to another state or that its
// diagnostics changed.
type LSPEvent struct {
	Type            string `json:"type"` // "state_changed" | "diagnostics_changed"
	Name            string `json:"name"`
	State           int    `json:"state"`
	Error           string `json:"error,omitempty"`
	DiagnosticCount int    `json:"diagnostic_count"`
}

// Toolbox is AgentGo's surface for code-aware tools and language servers: the
// tools the agent loop can call, plus the LSP state that the desktop, the
// @mention expander and the protocol adapters read.
type Toolbox interface {
	Tools() []Tool
	LSPStates() map[string]LSPClientInfo
	LSPDiagnostics(name string) any
	StartLSP(ctx context.Context, path string)
	StopAllLSPs(ctx context.Context)
	SetLSPEventListener(fn func(e LSPEvent))
	Close()
}
