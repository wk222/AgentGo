package agent

// Round 4 LLM trace proxy. It sits between AgentGo's real model client and the
// upstream API and does two jobs:
//
//  1. Forensics: records, for every model call, what the model was shown (tail
//     of the message list, tool count, whether the task text is still in
//     context) and the raw completion (content, finish_reason, parsed
//     tool_calls). This is what tells protocol artefacts (unparsed tool-call
//     markup) apart from genuine refusals, and proves the continuation turn
//     really carries the original task.
//  2. Pinning: optionally answers the FIRST completion locally with a recorded
//     stall text, so several strategies can be replayed from exactly the same
//     hesitant turn through AgentGo's production session/recovery path.
//
// The Authorization header is forwarded upstream but never logged.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"time"
)

// round4RespClip bounds a recorded completion. Pinned replay needs the stall
// text verbatim, so the bound is generous; anchors whose text reaches the old
// 1500-rune bound are not trusted to be complete (see round4AnchorsFromRecords).
const round4RespClip = 12000

type llmMsg struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	ToolCalls []string `json:"tool_calls,omitempty"`
}

type llmTurn struct {
	Index         int      `json:"index"`
	Pinned        bool     `json:"pinned,omitempty"`
	Rewritten     bool     `json:"rewritten,omitempty"`
	ReqMsgCount   int      `json:"req_msg_count"`
	ReqToolCount  int      `json:"req_tool_count"`
	TaskInContext bool     `json:"task_in_context"`
	ReqTail       []llmMsg `json:"req_tail,omitempty"`
	Status        int      `json:"status"`
	FinishReason  string   `json:"finish_reason,omitempty"`
	RespToolCalls []string `json:"resp_tool_calls,omitempty"`
	RespContent   string   `json:"resp_content,omitempty"`
	RespReasoning int      `json:"resp_reasoning_len,omitempty"`
	// Token usage as reported by the gateway (0 when it reports none). Useful to
	// spot hidden server-side loops: a single visible completion whose prompt
	// token count is a multiple of its siblings' was probably several upstream calls.
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	RawHead          string `json:"raw_head,omitempty"`
	UpstreamErr      string `json:"upstream_err,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
}

type llmProxy struct {
	srv      *httptest.Server
	upstream string
	task     string
	pinText  string
	client   *http.Client

	// rewrite, when set, transforms the first upstream-bound request (the one
	// after the pinned stall). It changes only the model's view for that call;
	// AgentGo's own durable transcript is untouched. Lab-only: it exists to
	// test the fabricated-rollback view that the audit rows describe but the
	// production ADK path does not deliver.
	rewrite func(body []byte) ([]byte, bool)

	// sysTransform, when set, rewrites the content of every system message in every
	// chat request before it is forwarded (banner ablation). Like rewrite it changes
	// only what the model is shown.
	sysTransform func(string) string

	mu        sync.Mutex
	turns     []llmTurn
	pinned    bool
	rewritten bool
}

// SetSystemTransform installs the per-request system-message transform.
func (p *llmProxy) SetSystemTransform(f func(string) string) {
	p.mu.Lock()
	p.sysTransform = f
	p.mu.Unlock()
}

// applySystemTransform returns body with every string system message mapped
// through f. Bodies that are not chat requests or fail to parse pass through.
func applySystemTransform(body []byte, f func(string) string) []byte {
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil {
		return body
	}
	raw, ok := top["messages"]
	if !ok {
		return body
	}
	var msgs []map[string]any
	if json.Unmarshal(raw, &msgs) != nil {
		return body
	}
	changed := false
	for _, m := range msgs {
		if role, _ := m["role"].(string); role != "system" {
			continue
		}
		if s, ok := m["content"].(string); ok {
			if ns := f(s); ns != s {
				m["content"] = ns
				changed = true
			}
		}
	}
	if !changed {
		return body
	}
	nm, err := json.Marshal(msgs)
	if err != nil {
		return body
	}
	top["messages"] = nm
	out, err := json.Marshal(top)
	if err != nil {
		return body
	}
	return out
}

// round4StripArmorBanner turns the red_team system prompt AgentGo builds under
// AGENTGO_ARMOR_BREAK into the plain assistant prompt: the "[Armor-break
// session]" banner and the "[Armor session memories]" block are cut and the
// profile name is reverted. It is the exact inverse of SessionMode.ModeHints'
// red_team addition (asserted by TestRound4StripBannerEqualsAssistantPrompt).
func round4StripArmorBanner(sys string) string {
	if i := strings.Index(sys, "\n[Armor-break session]"); i >= 0 {
		if j := strings.Index(sys[i:], "</system_rules>"); j >= 0 {
			sys = sys[:i] + "\n" + sys[i+j:] // wrapped prompt: keep the closing tag
		} else {
			sys = sys[:i] // bare hints block: the banner runs to the end
		}
	}
	sys = strings.ReplaceAll(sys, "[ModeProfile: red_team;", "[ModeProfile: assistant;")
	sys = strings.ReplaceAll(sys, "profile=red_team", "profile=assistant")
	return sys
}

// SetRewrite installs the first-live-request rewrite (see rewrite).
func (p *llmProxy) SetRewrite(f func(body []byte) ([]byte, bool)) {
	p.mu.Lock()
	p.rewrite = f
	p.mu.Unlock()
}

func newLLMProxy(upstream, taskText, pinText string) *llmProxy {
	p := &llmProxy{
		upstream: strings.TrimRight(upstream, "/"),
		task:     taskText,
		pinText:  pinText,
		client:   &http.Client{Timeout: 5 * time.Minute},
	}
	p.srv = httptest.NewServer(http.HandlerFunc(p.handle))
	return p
}

func (p *llmProxy) URL() string { return p.srv.URL }
func (p *llmProxy) Close()      { p.srv.Close() }

func (p *llmProxy) Turns() []llmTurn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]llmTurn(nil), p.turns...)
}

func (p *llmProxy) add(t llmTurn) {
	p.mu.Lock()
	t.Index = len(p.turns)
	p.turns = append(p.turns, t)
	p.mu.Unlock()
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// msgText flattens an OpenAI message "content" (string or parts array).
func msgText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, part := range parts {
			b.WriteString(part.Text)
		}
		return b.String()
	}
	return string(raw)
}

type chatReq struct {
	Stream   bool `json:"stream"`
	Messages []struct {
		Role      string          `json:"role"`
		Content   json.RawMessage `json:"content"`
		ToolCalls []struct {
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"messages"`
	Tools []json.RawMessage `json:"tools"`
}

func (p *llmProxy) describeRequest(body []byte) llmTurn {
	var req chatReq
	var t llmTurn
	if json.Unmarshal(body, &req) != nil {
		return t
	}
	t.ReqMsgCount = len(req.Messages)
	t.ReqToolCount = len(req.Tools)
	taskKey := strings.TrimSpace(p.task)
	for _, m := range req.Messages {
		if taskKey != "" && strings.Contains(msgText(m.Content), taskKey) {
			t.TaskInContext = true
		}
	}
	start := len(req.Messages) - 6
	if start < 0 {
		start = 0
	}
	for _, m := range req.Messages[start:] {
		lm := llmMsg{Role: m.Role, Content: clip(msgText(m.Content), 500)}
		for _, tc := range m.ToolCalls {
			lm.ToolCalls = append(lm.ToolCalls, tc.Function.Name)
		}
		t.ReqTail = append(t.ReqTail, lm)
	}
	return t
}

func (p *llmProxy) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	started := time.Now()
	isChat := strings.HasSuffix(r.URL.Path, "/chat/completions")

	doPin, doRewrite := false, false
	if isChat {
		p.mu.Lock()
		doPin = p.pinText != "" && !p.pinned
		if doPin {
			p.pinned = true
		}
		doRewrite = !doPin && p.rewrite != nil && !p.rewritten && (p.pinText == "" || p.pinned)
		if doRewrite {
			p.rewritten = true
		}
		p.mu.Unlock()
	}
	if isChat {
		p.mu.Lock()
		st := p.sysTransform
		p.mu.Unlock()
		if st != nil {
			body = applySystemTransform(body, st)
		}
	}
	rewrote := false
	if doRewrite {
		if nb, ok := p.rewrite(body); ok {
			body, rewrote = nb, true
		}
	}
	turn := p.describeRequest(body) // describes what the model is actually shown
	turn.Rewritten = rewrote
	var req chatReq
	_ = json.Unmarshal(body, &req)

	if isChat {
		if doPin {
			turn.Pinned = true
			turn.Status = 200
			turn.FinishReason = "stop"
			turn.RespContent = clip(p.pinText, round4RespClip)
			p.writePinned(w, req.Stream)
			turn.DurationMS = time.Since(started).Milliseconds()
			p.add(turn)
			return
		}
	}

	up, err := http.NewRequestWithContext(r.Context(), r.Method, p.upstream+r.URL.Path, bytes.NewReader(body))
	if err != nil {
		turn.UpstreamErr = err.Error()
		p.add(turn)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	for _, h := range []string{"Authorization", "Content-Type", "Accept"} {
		if v := r.Header.Get(h); v != "" {
			up.Header.Set(h, v)
		}
	}
	resp, err := p.client.Do(up)
	if err != nil {
		turn.UpstreamErr = err.Error()
		turn.DurationMS = time.Since(started).Milliseconds()
		p.add(turn)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	turn.Status = resp.StatusCode
	if isChat {
		p.parseCompletion(&turn, raw, req.Stream)
	}
	turn.DurationMS = time.Since(started).Milliseconds()
	p.add(turn)

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}

func (p *llmProxy) parseCompletion(t *llmTurn, raw []byte, stream bool) {
	if stream || bytes.HasPrefix(bytes.TrimSpace(raw), []byte("data:")) {
		var content strings.Builder
		names := map[string]bool{}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var ch struct {
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Function struct {
								Name string `json:"name"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(payload), &ch) != nil {
				continue
			}
			if ch.Usage != nil && ch.Usage.PromptTokens > 0 {
				t.PromptTokens, t.CompletionTokens = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
			}
			if len(ch.Choices) == 0 {
				continue
			}
			content.WriteString(ch.Choices[0].Delta.Content)
			for _, tc := range ch.Choices[0].Delta.ToolCalls {
				if tc.Function.Name != "" {
					names[tc.Function.Name] = true
				}
			}
			if ch.Choices[0].FinishReason != "" {
				t.FinishReason = ch.Choices[0].FinishReason
			}
		}
		t.RespContent = clip(content.String(), round4RespClip)
		for n := range names {
			t.RespToolCalls = append(t.RespToolCalls, n)
		}
		return
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Choices) == 0 {
		t.RawHead = clip(string(raw), 800)
		return
	}
	t.PromptTokens, t.CompletionTokens = out.Usage.PromptTokens, out.Usage.CompletionTokens
	c := out.Choices[0]
	t.FinishReason = c.FinishReason
	t.RespContent = clip(c.Message.Content, round4RespClip)
	t.RespReasoning = len([]rune(c.Message.ReasoningContent))
	for _, tc := range c.Message.ToolCalls {
		t.RespToolCalls = append(t.RespToolCalls, tc.Function.Name)
	}
}

func (p *llmProxy) writePinned(w http.ResponseWriter, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "round4-pinned", "object": "chat.completion", "created": 1, "model": "pinned",
			"choices": []any{map[string]any{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": p.pinText}}},
			"usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	chunk := func(delta map[string]any, finish any) string {
		b, _ := json.Marshal(map[string]any{
			"id": "round4-pinned", "object": "chat.completion.chunk", "created": 1, "model": "pinned",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		})
		return fmt.Sprintf("data: %s\n\n", b)
	}
	_, _ = io.WriteString(w, chunk(map[string]any{"role": "assistant", "content": p.pinText}, nil))
	_, _ = io.WriteString(w, chunk(map[string]any{}, "stop"))
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
}

var (
	textToolCallRe  = regexp.MustCompile(`(?s)^\s*[A-Za-z_][\w.\-]*\s*[\r\n]+\s*[\{\[]`)
	namedToolJSONRe = regexp.MustCompile(`\bsb_[a-z_]+\b\s*[\r\n:(]*\s*\{`)
)

// stallForensics classifies why a zero-tool-call model turn happened, from the
// raw completion rather than from AgentGo's post-processed text.
//
//	unparsed_toolcall_markup  the model emitted tool-call markup/JSON in content but
//	                          the gateway returned no structured tool_calls
//	blank_gap_artifact        content shows stripped-token gaps (runs of spaces) that
//	                          suggest removed tool-call special tokens
//	genuine_text              ordinary prose with no markup signs
func stallForensics(t llmTurn) string {
	c := t.RespContent
	markers := []string{"<tool_call", "tool_calls", "<｜tool", "<|tool", "function_call", "\"name\":", "```json"}
	for _, m := range markers {
		if strings.Contains(c, m) {
			return "unparsed_toolcall_markup"
		}
	}
	// A bare tool name followed by a JSON object ("sb_delete\n{...}") is a
	// tool call written as prose: the model meant to call, the protocol did
	// not carry it.
	if textToolCallRe.MatchString(c) || namedToolJSONRe.MatchString(c) {
		return "unparsed_toolcall_markup"
	}
	if strings.Contains(c, "          ") || strings.Contains(c, "\u3000\u3000\u3000") {
		return "blank_gap_artifact"
	}
	return "genuine_text"
}
