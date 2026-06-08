package gateway

import "context"

// ChatRequest is POST /api/v1/chat/stream body.
type ChatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// ChatResult is the final assistant payload after SSE stream ends.
type ChatResult struct {
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// Backend is implemented by bridge.Runtime (no import cycle).
type Backend interface {
	ChatModelName() string
	StreamChat(ctx context.Context, req ChatRequest, emit func(event string, data []byte) error) (*ChatResult, error)
	ReplayTaskEvents(ctx context.Context, taskID string, afterSeq int64, emit func(event string, data []byte) error) error
	RegisterTaskListener(taskID string, emit func(event string, data []byte)) (unregister func())
	ListWorkflows(ctx context.Context) ([]byte, error)
	GetWorkflowFlowgram(ctx context.Context, id string) ([]byte, error)
	SaveWorkflowFlowgram(ctx context.Context, id string, flowgramJSON []byte) error
	RunWorkflow(ctx context.Context, id, input string) (string, error)
	CancelSessionRun(ctx context.Context, sessionID string) bool
	ListInnerApps(ctx context.Context) ([]byte, error)
	GetInnerApp(ctx context.Context, name string) ([]byte, error)
	InvokeInnerApp(ctx context.Context, name, input, capability, action, payloadJSON string) ([]byte, error)
	GetInnerAppAsset(ctx context.Context, name, relPath string) ([]byte, string, error)
}
