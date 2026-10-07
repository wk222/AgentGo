// Package crushproto serves Crush's /v1 client/server protocol, so the stock
// Crush TUI (`CRUSH_CLIENT_SERVER=1 crush -H tcp://...`) can front a different
// brain. The protocol layer (Server) is separate from whoever runs the agent
// (Runner): the probe ships a scripted Runner, the real one bridges to AgentGo.
//
// Wire shapes were recorded from a real Crush server (scripts/crushproto) and
// are served from testdata fixtures where they are static.
package crushproto

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"
)

// Message is a Crush message SNAPSHOT: every "updated" event carries the whole
// parts list, not a delta.
type Message struct {
	Parts     []Part `json:"parts"`
	ID        string `json:"id"`
	Role      string `json:"role"` // user | assistant | tool
	SessionID string `json:"session_id"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Part is one typed element of a message. Types and data shapes:
//
//	text        {text}
//	reasoning   {thinking, signature, started_at, finished_at?}
//	tool_call   {id, name, input, finished?}
//	tool_result {tool_call_id, name, content, metadata, is_error}
//	finish      {reason: stop|tool_use|end_turn, time}
type Part struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// Session mirrors Crush's session record.
type Session struct {
	ID               string  `json:"id"`
	ParentSessionID  string  `json:"parent_session_id"`
	Title            string  `json:"title"`
	MessageCount     int     `json:"message_count"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	SummaryMessageID string  `json:"summary_message_id"`
	Cost             float64 `json:"cost"`
	CreatedAt        int64   `json:"created_at"`
	UpdatedAt        int64   `json:"updated_at"`
	IsBusy           bool    `json:"is_busy"`
	AttachedClients  int     `json:"attached_clients"`
}

// PermissionRequest is the payload of a permission_request event and the body
// element of POST .../permissions/grant.
type PermissionRequest struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

// RunRequest is the body of POST .../agent.
type RunRequest struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Prompt    string `json:"prompt"`
	Channel   string `json:"channel,omitempty"`
	Hidden    bool   `json:"hidden_user_message,omitempty"`
}

// envelope is the SSE frame: {"type":kind,"payload":{"type":op,"payload":entity}}.
func envelope(kind, op string, entity any) ([]byte, error) {
	return json.Marshal(map[string]any{
		"type":    kind,
		"payload": map[string]any{"type": op, "payload": entity},
	})
}

func nowSec() int64 { return time.Now().Unix() }

// newID returns a random UUIDv4 (Crush validates client ids as UUIDs).
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
