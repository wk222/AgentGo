package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/schema"
)

// sse renders events the way the Responses API frames them.
func sse(events ...string) string {
	var b strings.Builder
	for _, e := range events {
		var h struct{ Type string }
		_ = json.Unmarshal([]byte(e), &h)
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", h.Type, e)
	}
	return b.String()
}

func newRespServer(t *testing.T, status int, body string, captured *[]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			*captured, _ = io.ReadAll(r.Body)
		}
		if r.URL.Path != "/v1/responses" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testRespModel(srv *httptest.Server) *responsesModel {
	return newResponsesModel(LLMSettings{APIBase: srv.URL + "/v1/", APIKey: "k"}, "gpt-6-luna", 5*time.Second, nil)
}

const completedEv = `{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":35,"output_tokens":92,"total_tokens":127,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":57}}}}`

func TestResponsesReasoningSummaryTextAndUsage(t *testing.T) {
	t.Setenv("AGENTGO_REASONING_EFFORT", "high")
	var body []byte
	srv := newRespServer(t, 200, sse(
		`{"type":"response.created","response":{"status":"in_progress"}}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning"}}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"Clarifying "}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"the riddle."}`,
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":1,"delta":"Then add."}`,
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"message"}}`,
		`{"type":"response.output_text.delta","output_index":1,"delta":"27 "}`,
		`{"type":"response.output_text.delta","output_index":1,"delta":"sheep"}`,
		completedEv,
	), &body)
	m := testRespModel(srv)

	got, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("how many?")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "27 sheep" {
		t.Errorf("content = %q", got.Content)
	}
	if got.ReasoningContent != "Clarifying the riddle.\n\nThen add." {
		t.Errorf("reasoning = %q", got.ReasoningContent)
	}
	u := got.ResponseMeta.Usage
	if u == nil || u.PromptTokens != 35 || u.CompletionTokens != 92 || u.TotalTokens != 127 ||
		u.CompletionTokensDetails.ReasoningTokens != 57 || u.PromptTokenDetails.CachedTokens != 4 {
		t.Errorf("usage = %+v", u)
	}
	if got.ResponseMeta.FinishReason != "stop" {
		t.Errorf("finish = %q", got.ResponseMeta.FinishReason)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	r, _ := req["reasoning"].(map[string]any)
	if req["stream"] != true || req["store"] != false || r["effort"] != "high" || r["summary"] != "auto" {
		t.Errorf("request = %s", body)
	}
}

// Stream must deliver reasoning chunks before the text, one chunk per delta.
func TestResponsesStreamOrdersReasoningBeforeText(t *testing.T) {
	srv := newRespServer(t, 200, sse(
		`{"type":"response.reasoning_summary_text.delta","output_index":0,"summary_index":0,"delta":"think"}`,
		`{"type":"response.output_text.delta","output_index":1,"delta":"answer"}`,
		completedEv,
	), nil)
	sr, err := testRespModel(srv).Stream(context.Background(), []*schema.Message{schema.UserMessage("q")})
	if err != nil {
		t.Fatal(err)
	}
	defer sr.Close()
	var kinds []string
	for {
		c, err := sr.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case c.ReasoningContent != "":
			kinds = append(kinds, "R:"+c.ReasoningContent)
		case c.Content != "":
			kinds = append(kinds, "T:"+c.Content)
		case c.ResponseMeta != nil:
			kinds = append(kinds, "END")
		}
	}
	if strings.Join(kinds, ",") != "R:think,T:answer,END" {
		t.Errorf("order = %v", kinds)
	}
}

func TestResponsesToolCallsStreamedAndFromDoneEvent(t *testing.T) {
	srv := newRespServer(t, 200, sse(
		// call 0: arguments streamed in deltas
		`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_1","call_id":"call_A","name":"read_file"}}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"path\":"}`,
		`{"type":"response.function_call_arguments.delta","output_index":1,"delta":"\"a.go\"}"}`,
		`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_A","name":"read_file","arguments":"{\"path\":\"a.go\"}"}}`,
		// call 1: arguments only on the done event
		`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_2","call_id":"call_B","name":"grep"}}`,
		`{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","call_id":"call_B","name":"grep","arguments":"{\"q\":\"x\"}"}}`,
		completedEv,
	), nil)
	got, err := testRespModel(srv).Generate(context.Background(), []*schema.Message{schema.UserMessage("go")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", got.ToolCalls)
	}
	a, b := got.ToolCalls[0], got.ToolCalls[1]
	if a.ID != "call_A" || a.Function.Name != "read_file" || a.Function.Arguments != `{"path":"a.go"}` {
		t.Errorf("call A = %+v", a)
	}
	if b.ID != "call_B" || b.Function.Name != "grep" || b.Function.Arguments != `{"q":"x"}` {
		t.Errorf("call B = %+v", b)
	}
	if got.ResponseMeta.FinishReason != "tool_calls" {
		t.Errorf("finish = %q", got.ResponseMeta.FinishReason)
	}
}

func TestResponsesRequestMapping(t *testing.T) {
	var body []byte
	srv := newRespServer(t, 200, sse(completedEv), &body)
	m, _ := testRespModel(srv).WithTools([]*schema.ToolInfo{{Name: "read_file", Desc: "reads"}})

	in := []*schema.Message{
		schema.SystemMessage("be brief"),
		schema.UserMessage("read it"),
		{Role: schema.Assistant, Content: "ok", ToolCalls: []schema.ToolCall{{
			ID: "call_A", Type: "function", Function: schema.FunctionCall{Name: "read_file"}, // empty args
		}}},
		schema.ToolMessage("file body", "call_A"),
	}
	if _, err := m.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}

	var req struct {
		Input []map[string]any `json:"input"`
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0]["type"] != "function" || req.Tools[0]["name"] != "read_file" {
		t.Errorf("tools = %v", req.Tools)
	}
	if _, ok := req.Tools[0]["parameters"].(map[string]any); !ok {
		t.Errorf("parameters missing: %v", req.Tools[0])
	}
	if len(req.Input) != 5 {
		t.Fatalf("input items = %d: %s", len(req.Input), body)
	}
	if req.Input[0]["role"] != "system" || req.Input[1]["role"] != "user" || req.Input[2]["role"] != "assistant" {
		t.Errorf("roles = %v", req.Input[:3])
	}
	fc := req.Input[3]
	if fc["type"] != "function_call" || fc["call_id"] != "call_A" || fc["arguments"] != "{}" {
		t.Errorf("function_call = %v", fc)
	}
	if _, has := fc["id"]; has {
		t.Errorf("function_call must not carry an id: %v", fc)
	}
	out := req.Input[4]
	if out["type"] != "function_call_output" || out["call_id"] != "call_A" || out["output"] != "file body" {
		t.Errorf("function_call_output = %v", out)
	}
}

type usageRec struct{ ch chan [2]int }

func (u usageRec) ModelUsage(prompt, completion int) { u.ch <- [2]int{prompt, completion} }

// Token usage must reach the UsageObserver on both the streaming (normal) and
// the blocking path; the Crush sidebar's token counter depends on it.
func TestResponsesReportsUsageThroughCallbacks(t *testing.T) {
	srv := newRespServer(t, 200, sse(`{"type":"response.output_text.delta","delta":"hi"}`, completedEv), nil)
	m := testRespModel(srv)

	for _, mode := range []string{"stream", "generate"} {
		obs := usageRec{ch: make(chan [2]int, 4)}
		ctx := callbacks.InitCallbacks(context.Background(),
			&callbacks.RunInfo{Name: "t", Type: m.GetType(), Component: components.ComponentOfChatModel},
			newUsageCallbackHandler(obs))
		in := []*schema.Message{schema.UserMessage("q")}
		if mode == "stream" {
			sr, err := m.Stream(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			for {
				if _, err := sr.Recv(); err != nil {
					break
				}
			}
			sr.Close()
		} else if _, err := m.Generate(ctx, in); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-obs.ch:
			if got != [2]int{35, 92} {
				t.Errorf("%s: usage = %v, want [35 92]", mode, got)
			}
		case <-time.After(3 * time.Second):
			t.Errorf("%s: usage never reported", mode)
		}
	}
}

func TestResponsesHTTPErrorSurfaces(t *testing.T) {
	srv := newRespServer(t, 400, `{"error":{"message":"bad tool schema"}}`, nil)
	_, err := testRespModel(srv).Generate(context.Background(), []*schema.Message{schema.UserMessage("x")})
	if err == nil || !strings.Contains(err.Error(), "HTTP 400") || !strings.Contains(err.Error(), "bad tool schema") {
		t.Fatalf("err = %v", err)
	}
}

func TestResponsesTruncatedStreamIsAnError(t *testing.T) {
	srv := newRespServer(t, 200, sse(`{"type":"response.output_text.delta","delta":"par"}`), nil)
	_, err := testRespModel(srv).Generate(context.Background(), []*schema.Message{schema.UserMessage("x")})
	if err == nil || !strings.Contains(err.Error(), "before response.completed") {
		t.Fatalf("err = %v", err)
	}
}

func TestResponsesFailedEventSurfaces(t *testing.T) {
	srv := newRespServer(t, 200, sse(`{"type":"response.failed","response":{"error":{"message":"content filtered"}}}`), nil)
	_, err := testRespModel(srv).Generate(context.Background(), []*schema.Message{schema.UserMessage("x")})
	if err == nil || !strings.Contains(err.Error(), "content filtered") {
		t.Fatalf("err = %v", err)
	}
}

func TestResponsesEnabledOnlyWhenOptedIn(t *testing.T) {
	t.Setenv("AGENTGO_LLM_API", "")
	if responsesEnabled() {
		t.Fatal("must be off by default")
	}
	t.Setenv("AGENTGO_LLM_API", "Responses")
	if !responsesEnabled() {
		t.Fatal("must be on when AGENTGO_LLM_API=responses (case-insensitive)")
	}
}
