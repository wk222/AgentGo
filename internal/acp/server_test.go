package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dummyHandler struct{}

func (d *dummyHandler) HandlePrompt(ctx context.Context, sessionID, prompt string, emit func(update SessionUpdate)) (string, error) {
	emit(SessionUpdate{
		SessionUpdate: "thought",
		Content:       &UpdateContent{Type: "text", Text: "Analyzing request..."},
	})
	emit(SessionUpdate{
		SessionUpdate: "agent_message_chunk",
		Content:       &UpdateContent{Type: "text", Text: "Hello "},
	})
	emit(SessionUpdate{
		SessionUpdate: "agent_message_chunk",
		Content:       &UpdateContent{Type: "text", Text: "from AgentGo!"},
	})
	return "Hello from AgentGo!", nil
}

func (d *dummyHandler) CancelSession(sessionID string) bool {
	return true
}

func (d *dummyHandler) NewSession(params NewSessionParams) (*NewSessionResult, error) {
	return &NewSessionResult{
		SessionID: params.SessionID,
		Modes: &SessionModes{
			CurrentModeID: "coder",
			AvailableModes: []ModeItem{
				{ID: "coder", Name: "Coder"},
			},
		},
	}, nil
}

func (d *dummyHandler) SetSessionMode(sessionID, modeID string) error {
	return nil
}

func (d *dummyHandler) CloseSession(sessionID string) error {
	return nil
}

func (d *dummyHandler) GenerateInlineEdit(filePath, language, selectedText, contextBefore, contextAfter, instruction string) (map[string]any, error) {
	return map[string]any{
		"success":     true,
		"replacement": "fmt.Println(\"fixed\")",
		"summary":     "fixed bug",
	}, nil
}

func (d *dummyHandler) GetSystemStatus() (map[string]any, error) {
	return map[string]any{
		"model":  "deepseek-chat",
		"mode":   "coder",
		"status": "ready",
	}, nil
}

func (d *dummyHandler) TerminalQuickFix(command, output string, exitCode int, cwd string) (map[string]any, error) {
	return map[string]any{
		"success":          true,
		"explanation":      "缺少依赖",
		"suggestedCommand": "pnpm install",
	}, nil
}

func (d *dummyHandler) GenerateCompletion(prefix, suffix, language string) (map[string]any, error) {
	return map[string]any{
		"items": []map[string]any{
			{"insertText": "return true"},
		},
	}, nil
}

func (d *dummyHandler) FixProblem(filePath, code, errorMessage string) (map[string]any, error) {
	return map[string]any{
		"success":     true,
		"replacement": "fixedCode()",
		"summary":     "fixed error",
	}, nil
}

func TestServer_FullACPLifecycle(t *testing.T) {
	in := bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"newSession","params":{"sessionId":"test-session-1"}}` + "\n" +
			`{"jsonrpc":"2.0","id":3,"method":"prompt","params":{"sessionId":"test-session-1","prompt":"hello"}}` + "\n" +
			`{"jsonrpc":"2.0","id":4,"method":"setSessionMode","params":{"sessionId":"test-session-1","modeId":"coder"}}` + "\n" +
			`{"jsonrpc":"2.0","id":5,"method":"inlineEdit","params":{"instruction":"fix"}}` + "\n" +
			`{"jsonrpc":"2.0","id":6,"method":"getSystemStatus","params":{}}` + "\n" +
			`{"jsonrpc":"2.0","id":7,"method":"terminalQuickFix","params":{"command":"npm test","output":"err","exitCode":1}}` + "\n" +
			`{"jsonrpc":"2.0","id":8,"method":"cancel","params":{"sessionId":"test-session-1"}}` + "\n" +
			`{"jsonrpc":"2.0","id":9,"method":"closeSession","params":{"sessionId":"test-session-1"}}` + "\n",
	)
	var out bytes.Buffer

	srv := NewServer(&dummyHandler{}, in, &out)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := srv.Serve(ctx)
	require.NoError(t, err)

	time.Sleep(50 * time.Millisecond)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 6)

	// Check initialize response
	var initResp Response
	err = json.Unmarshal([]byte(lines[0]), &initResp)
	require.NoError(t, err)
	assert.Equal(t, float64(1), initResp.ID)
	initResultMap, ok := initResp.Result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), initResultMap["protocolVersion"])

	// Check newSession response
	var newSessResp Response
	err = json.Unmarshal([]byte(lines[1]), &newSessResp)
	require.NoError(t, err)
	assert.Equal(t, float64(2), newSessResp.ID)
}
