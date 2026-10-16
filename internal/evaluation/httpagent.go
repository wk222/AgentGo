package evaluation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPAgent drives a running `agentgo --serve` over its SSE chat endpoint and reads
// per-run consumption from the final "done" event.
type HTTPAgent struct {
	BaseURL string       // e.g. http://127.0.0.1:8787
	Token   string       // bearer token from agentgo-serve.json
	Client  *http.Client // optional; defaults to a client without overall timeout
	Harness string       // optional preset id (minimal/standard/ultimate/creative) applied to every session
}

type doneEvent struct {
	Content string `json:"content"`
	Error   string `json:"error"`
	Usage   *struct {
		ModelCalls       int `json:"model_calls"`
		PromptTokens     int `json:"prompt_tokens"`
		CachedTokens     int `json:"cached_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		ReasoningTokens  int `json:"reasoning_tokens"`
		ToolCalls        int `json:"tool_calls"`
	} `json:"usage"`
}

// Generate implements AgentGenerator.
func (a *HTTPAgent) Generate(ctx context.Context, sessionID, input string) (string, error) {
	out, _, err := a.GenerateMeasured(ctx, sessionID, input)
	return out, err
}

// GenerateMeasured implements MeasuredAgent.
func (a *HTTPAgent) GenerateMeasured(ctx context.Context, sessionID, input string) (string, Metrics, error) {
	payload := map[string]string{"session_id": sessionID, "message": input}
	if a.Harness != "" {
		payload["harness"] = a.Harness
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.BaseURL, "/")+"/api/v1/chat/stream", bytes.NewReader(body))
	if err != nil {
		return "", Metrics{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	cli := a.Client
	if cli == nil {
		cli = &http.Client{}
	}
	resp, err := cli.Do(req)
	if err != nil {
		return "", Metrics{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", Metrics{}, fmt.Errorf("gateway HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return parseChatSSE(resp.Body)
}

// parseChatSSE reads the gateway stream: chunk* [interrupt] (error | done).
func parseChatSSE(r io.Reader) (string, Metrics, error) {
	var (
		event     string
		chunks    strings.Builder
		interrupt string
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			switch event {
			case "chunk":
				var c struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal([]byte(data), &c) == nil {
					chunks.WriteString(c.Delta)
				}
			case "interrupt":
				var in struct {
					ToolName string `json:"tool_name"`
				}
				_ = json.Unmarshal([]byte(data), &in)
				interrupt = in.ToolName
			case "error":
				var e struct {
					Error string `json:"error"`
				}
				_ = json.Unmarshal([]byte(data), &e)
				return chunks.String(), Metrics{}, fmt.Errorf("agent error: %s", e.Error)
			case "done":
				var d doneEvent
				if err := json.Unmarshal([]byte(data), &d); err != nil {
					return chunks.String(), Metrics{}, fmt.Errorf("bad done event: %w", err)
				}
				out := d.Content
				if out == "" {
					out = chunks.String()
				}
				var m Metrics
				if u := d.Usage; u != nil {
					m = Metrics{ModelCalls: u.ModelCalls, PromptTokens: u.PromptTokens, CachedTokens: u.CachedTokens,
						CompletionTokens: u.CompletionTokens, ReasoningTokens: u.ReasoningTokens, ToolCalls: u.ToolCalls}
				}
				if d.Error != "" {
					return out, m, fmt.Errorf("agent error: %s", d.Error)
				}
				if interrupt != "" {
					return out, m, fmt.Errorf("run paused for approval of tool %q; evaluation cases must not need approval", interrupt)
				}
				return out, m, nil
			}
		case line == "":
			event = ""
		}
	}
	if err := sc.Err(); err != nil {
		return chunks.String(), Metrics{}, err
	}
	return chunks.String(), Metrics{}, fmt.Errorf("stream ended without a done event")
}

// DefaultRunTag returns a tag that makes session ids unique per evaluation.
func DefaultRunTag() string { return time.Now().Format("20060102T150405") }
