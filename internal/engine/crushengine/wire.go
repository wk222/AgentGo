package crushengine

import "encoding/json"

// Wire types mirror a minimal subset of Crush's /v1 API (v0.97.x). They are
// decoded permissively: unknown fields are ignored, so Crush minor upgrades
// keep working. See docs/crush-audit.md for the source of truth.

// envelope is one SSE "data:" record.
type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// pubEvent is Crush's pubsub.Event[T] wrapper (created/updated/deleted).
type pubEvent[T any] struct {
	Type    string `json:"type"`
	Payload T      `json:"payload"`
}

const (
	payloadMessage           = "message"
	payloadFile              = "file"
	payloadPermissionRequest = "permission_request"
	payloadQuestionRequest   = "question_batch_request"
	payloadRunComplete       = "run_complete"
	payloadAgentEvent        = "agent_event"
)

type wirePart struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type wireMessage struct {
	ID        string     `json:"id"`
	Role      string     `json:"role"`
	SessionID string     `json:"session_id"`
	Parts     []wirePart `json:"parts"`
}

type textData struct {
	Text   string `json:"text"`
	Hidden bool   `json:"hidden"`
}

type reasoningData struct {
	Thinking string `json:"thinking"`
}

type toolCallData struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Input    string `json:"input"`
	Finished bool   `json:"finished"`
}

type toolResultData struct {
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error"`
}

type wireFile struct {
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Version   int64  `json:"version"`
}

type wirePermission struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	ToolCallID  string          `json:"tool_call_id"`
	ToolName    string          `json:"tool_name"`
	Description string          `json:"description"`
	Action      string          `json:"action"`
	Path        string          `json:"path"`
	Params      json.RawMessage `json:"params"`
}

type wireQuestion struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
}

type wireRunComplete struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
	Error     string `json:"error"`
	Cancelled bool   `json:"cancelled"`
}

type wireAgentEvent struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Type      string `json:"type"`
	Error     any    `json:"error"`
}

// Permission actions accepted by POST /permissions/grant.
const (
	permAllow           = "allow"
	permAllowForSession = "allow_session"
	permDeny            = "deny"
)

// workspaceInfo is the subset of proto.Workspace we need.
type workspaceInfo struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type sessionInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type apiError struct {
	Error string `json:"error"`
}
