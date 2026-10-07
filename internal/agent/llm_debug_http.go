package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// debugLLMEnabled turns on message-shape logging for LLM requests
// (AGENTGO_DEBUG_LLM=1). It logs only roles and tool-call ids, never headers,
// keys or message bodies.
func debugLLMEnabled() bool { return os.Getenv("AGENTGO_DEBUG_LLM") == "1" }

// debugf logs one shape line. AGENTGO_DEBUG_LLM_FILE appends to that file
// instead of the global logger, which an embedded TUI silences.
func debugf(format string, args ...any) {
	line := fmt.Sprintf("[llm-debug] "+format, args...)
	if p := os.Getenv("AGENTGO_DEBUG_LLM_FILE"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			_, _ = f.WriteString(time.Now().Format("15:04:05.000 ") + line + "\n")
			_ = f.Close()
			return
		}
	}
	log.Print(line)
}

// DebugLogf is debugf for other packages; a no-op unless AGENTGO_DEBUG_LLM=1.
func DebugLogf(format string, args ...any) {
	if debugLLMEnabled() {
		debugf(format, args...)
	}
}

type llmShapeTransport struct{ next http.RoundTripper }

func newLLMShapeClient() *http.Client {
	return &http.Client{Transport: llmShapeTransport{next: http.DefaultTransport}}
}

func (t llmShapeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil && req.Method == http.MethodPost {
		raw, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err == nil {
			log.Printf("[llm-debug] %s", describeMessages(raw))
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		req.ContentLength = int64(len(raw))
	}
	return t.next.RoundTrip(req)
}

// describeMessages renders e.g. "user | assistant{call_1,call_2} | tool<call_1>".
func describeMessages(raw []byte) string {
	var body struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCallID string `json:"tool_call_id"`
			ToolCalls  []struct {
				ID       string `json:"id"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "unparsable request body"
	}
	parts := make([]string, 0, len(body.Messages))
	for _, m := range body.Messages {
		switch {
		case len(m.ToolCalls) > 0:
			ids := make([]string, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				ids = append(ids, c.Function.Name+":"+c.ID)
			}
			parts = append(parts, fmt.Sprintf("%s{%s}", m.Role, strings.Join(ids, ",")))
		case m.ToolCallID != "":
			parts = append(parts, fmt.Sprintf("%s<%s>", m.Role, m.ToolCallID))
		default:
			parts = append(parts, m.Role)
		}
	}
	return strings.Join(parts, " | ")
}
