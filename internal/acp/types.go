package acp

import (
	"encoding/json"
)

// JSON-RPC 2.0 Request envelope.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSON-RPC 2.0 Response envelope.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id,omitempty"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// JSON-RPC 2.0 Notification envelope.
type Notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// Error represents a JSON-RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// InitializeParams contains client info.
type InitializeParams struct {
	ProtocolVersion    int            `json:"protocolVersion,omitempty"`
	ClientName         string         `json:"client_name,omitempty"`
	ClientVersion      string         `json:"client_version,omitempty"`
	ClientCapabilities map[string]any `json:"clientCapabilities,omitempty"`
	ClientInfo         *EntityInfo    `json:"clientInfo,omitempty"`
}

// EntityInfo provides name and version of agent or client.
type EntityInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult returns standard ACP server capabilities.
type InitializeResult struct {
	ProtocolVersion   int            `json:"protocolVersion"`
	AgentCapabilities map[string]any `json:"agentCapabilities"`
	AgentInfo         EntityInfo     `json:"agentInfo"`
	AuthMethods       []any          `json:"authMethods"`
}

// ModeItem describes an available agent execution mode.
type ModeItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SessionModes holds available and active modes.
type SessionModes struct {
	CurrentModeID  string     `json:"currentModeId"`
	AvailableModes []ModeItem `json:"availableModes"`
}

// ConfigOptionItem represents a configurable knob (e.g. model, temperature).
type ConfigOptionItem struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Type         string              `json:"type"` // e.g. "select"
	Category     string              `json:"category,omitempty"`
	CurrentValue string              `json:"currentValue"`
	Options      []ConfigOptionEntry `json:"options,omitempty"`
}

// ConfigOptionEntry option in a select knob.
type ConfigOptionEntry struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

// NewSessionParams defines session creation options.
type NewSessionParams struct {
	SessionID       string   `json:"sessionId,omitempty"`
	WorkspaceTarget string   `json:"workspaceTarget,omitempty"`
	CWD             string   `json:"cwd,omitempty"`
	MCPServers      []any    `json:"mcpServers,omitempty"`
	Mode            string   `json:"mode,omitempty"`
}

// NewSessionResult returns created session metadata.
type NewSessionResult struct {
	SessionID     string             `json:"sessionId"`
	Modes         *SessionModes      `json:"modes,omitempty"`
	ConfigOptions []ConfigOptionItem `json:"configOptions,omitempty"`
}

// PromptBlock represents content element in prompt array.
type PromptBlock struct {
	Type string `json:"type"` // e.g. "text"
	Text string `json:"text,omitempty"`
}

// PromptParams sends a prompt to the agent.
type PromptParams struct {
	SessionID string          `json:"sessionId"`
	Prompt    json.RawMessage `json:"prompt"`
}

// ExtractPromptText parses prompt from string or array of blocks.
func (p *PromptParams) ExtractPromptText() string {
	if len(p.Prompt) == 0 {
		return ""
	}
	// Try raw string
	var str string
	if err := json.Unmarshal(p.Prompt, &str); err == nil {
		return str
	}
	// Try blocks
	var blocks []PromptBlock
	if err := json.Unmarshal(p.Prompt, &blocks); err == nil {
		var res string
		for _, b := range blocks {
			if b.Type == "text" || b.Type == "" {
				res += b.Text
			}
		}
		return res
	}
	return string(p.Prompt)
}

// PromptResult completes the prompt turn.
type PromptResult struct {
	StopReason string `json:"stopReason"` // e.g. "end_turn", "cancelled"
}

// SessionUpdateNotification payload for sessionUpdate events.
type SessionUpdateNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// SessionUpdate content payload (agent message chunk, thought, or tool call).
type SessionUpdate struct {
	SessionUpdate string         `json:"sessionUpdate"` // "agent_message_chunk" | "thought" | "tool_call" | "current_mode_update"
	Content       *UpdateContent `json:"content,omitempty"`
	ToolCall      *ToolCallInfo  `json:"toolCall,omitempty"`
	CurrentModeID string         `json:"currentModeId,omitempty"`
}

// UpdateContent text content.
type UpdateContent struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

// ToolCallInfo metadata for a tool invocation.
type ToolCallInfo struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
}

// SetSessionModeParams changes active mode.
type SetSessionModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

// CancelParams cancels an active session run.
type CancelParams struct {
	SessionID string `json:"sessionId"`
}
