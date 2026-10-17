package coderuntime

import (
	"context"
	"time"
)

// ToolResult is the output of one toolbox tool call.
type ToolResult struct {
	Content string
	IsError bool
}

// Tool is a framework-neutral code tool definition (view, edit, grep, ...).
// Schema is a complete JSON Schema object ({"type":"object",...}).
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	Parallel    bool
	// Run executes the tool. argsJSON is the model's JSON arguments.
	Run func(ctx context.Context, sessionID, callID, argsJSON string) (ToolResult, error)
}

// LSPClientInfo holds information about an LSP client's state.
type LSPClientInfo struct {
	Name            string    `json:"name"`
	State           int       `json:"state"`
	Error           string    `json:"error,omitempty"`
	DiagnosticCount int       `json:"diagnostic_count"`
	ConnectedAt     time.Time `json:"connected_at"`
}

// LSPEvent represents a change in LSP server state or diagnostics.
type LSPEvent struct {
	Type            string `json:"type"` // "state_changed" | "diagnostics_changed"
	Name            string `json:"name"`
	State           int    `json:"state"`
	Error           string `json:"error,omitempty"`
	DiagnosticCount int    `json:"diagnostic_count"`
}

// Toolbox is AgentGo's own surface for code-aware tools and language servers:
// the tools the agent loop can call, plus the LSP state the desktop, the
// @mention expander and protocol adapters read. Implementations live behind
// this interface so the vendored Crush toolbox (internal/codetools.Wrap) can
// be replaced piece by piece (docs/plan-crush-digestion.md, D1-D3).
type Toolbox interface {
	Tools() []Tool
	LSPStates() map[string]LSPClientInfo
	LSPDiagnostics(name string) any
	StartLSP(ctx context.Context, path string)
	StopAllLSPs(ctx context.Context)
	SetLSPEventListener(fn func(e LSPEvent))
	Close()
}
