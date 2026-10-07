package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// responsesModel is a model.ToolCallingChatModel over the OpenAI Responses API
// (POST {base}/responses, SSE). It exists because reasoning models such as
// Azure gpt-5 / gpt-6-luna report reasoning tokens on /chat/completions but never
// return the reasoning text; only /responses can stream the reasoning summary.
//
// The summary is mapped to schema.Message.ReasoningContent, so everything
// downstream (drainADKEvents -> ReasoningObserver -> Crush "thinking" part) is
// unchanged.
//
// Opt-in: AGENTGO_LLM_API=responses.
type responsesModel struct {
	baseURL string
	apiKey  string
	model   string
	effort  string // "" = model default
	summary string // reasoning.summary: auto|concise|detailed
	client  *http.Client
	tools   []*schema.ToolInfo
}

// responsesEnabled reports whether the Responses channel is selected.
func responsesEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("AGENTGO_LLM_API")), "responses")
}

func newResponsesModel(cfg LLMSettings, modelName string, timeout time.Duration, hc *http.Client) *responsesModel {
	if hc == nil {
		// No overall Timeout: it would cut long streams. The context bounds the call;
		// the header timeout bounds a dead endpoint.
		hc = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ResponseHeaderTimeout: timeout,
			TLSHandshakeTimeout:   15 * time.Second,
			IdleConnTimeout:       60 * time.Second,
		}}
	}
	summary := strings.TrimSpace(os.Getenv("AGENTGO_REASONING_SUMMARY"))
	if summary == "" {
		summary = "auto"
	}
	return &responsesModel{
		baseURL: strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/"),
		apiKey:  cfg.APIKey,
		model:   modelName,
		effort:  strings.TrimSpace(os.Getenv("AGENTGO_REASONING_EFFORT")),
		summary: summary,
		client:  hc,
	}
}

// WithTools returns a copy with tools bound (the receiver is never mutated).
func (m *responsesModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	c := *m
	c.tools = append([]*schema.ToolInfo(nil), tools...)
	return &c, nil
}

// GetType / IsCallbacksEnabled: this model fires its own eino callbacks (like the
// stock openai model) so the framework does not wrap it. The wrapper would only
// see a bare *schema.Message and token usage (UsageObserver, trace) would be lost.
func (m *responsesModel) GetType() string          { return "OpenAIResponses" }
func (m *responsesModel) IsCallbacksEnabled() bool { return true }

func (m *responsesModel) callbackInput(input []*schema.Message, co *model.Options) *model.CallbackInput {
	name := m.model
	if co.Model != nil && *co.Model != "" {
		name = *co.Model
	}
	return &model.CallbackInput{Messages: input, Tools: co.Tools, Config: &model.Config{Model: name}}
}

func callbackUsage(meta *schema.ResponseMeta) *model.TokenUsage {
	if meta == nil || meta.Usage == nil {
		return nil
	}
	u := meta.Usage
	return &model.TokenUsage{
		PromptTokens:            u.PromptTokens,
		PromptTokenDetails:      model.PromptTokenDetails{CachedTokens: u.PromptTokenDetails.CachedTokens},
		CompletionTokens:        u.CompletionTokens,
		TotalTokens:             u.TotalTokens,
		CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: u.CompletionTokensDetails.ReasoningTokens},
	}
}

func (m *responsesModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (out *schema.Message, err error) {
	co := model.GetCommonOptions(&model.Options{Tools: m.tools}, opts...)
	ctx = callbacks.EnsureRunInfo(ctx, m.GetType(), components.ComponentOfChatModel)
	cbIn := m.callbackInput(input, co)
	ctx = callbacks.OnStart(ctx, cbIn)
	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	sr, err := m.open(ctx, input, co)
	if err != nil {
		return nil, err
	}
	defer sr.Close()
	var chunks []*schema.Message
	for {
		c, rerr := sr.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return nil, rerr
		}
		chunks = append(chunks, c)
	}
	if len(chunks) == 0 {
		return nil, errors.New("responses: empty response")
	}
	out, err = schema.ConcatMessages(chunks)
	if err != nil {
		return nil, err
	}
	callbacks.OnEnd(ctx, &model.CallbackOutput{Message: out, Config: cbIn.Config, TokenUsage: callbackUsage(out.ResponseMeta)})
	return out, nil
}

func (m *responsesModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (outStream *schema.StreamReader[*schema.Message], err error) {
	co := model.GetCommonOptions(&model.Options{Tools: m.tools}, opts...)
	ctx = callbacks.EnsureRunInfo(ctx, m.GetType(), components.ComponentOfChatModel)
	cbIn := m.callbackInput(input, co)
	ctx = callbacks.OnStart(ctx, cbIn)
	defer func() {
		if err != nil {
			callbacks.OnError(ctx, err)
		}
	}()

	sr, err := m.open(ctx, input, co)
	if err != nil {
		return nil, err
	}
	_, nsr := callbacks.OnEndWithStreamOutput(ctx, schema.StreamReaderWithConvert(sr,
		func(msg *schema.Message) (callbacks.CallbackOutput, error) {
			return &model.CallbackOutput{Message: msg, Config: cbIn.Config, TokenUsage: callbackUsage(msg.ResponseMeta)}, nil
		}))
	return schema.StreamReaderWithConvert(nsr, func(src callbacks.CallbackOutput) (*schema.Message, error) {
		return src.(*model.CallbackOutput).Message, nil
	}), nil
}

// open sends the request and returns the raw message stream (no callbacks).
func (m *responsesModel) open(ctx context.Context, input []*schema.Message, co *model.Options) (*schema.StreamReader[*schema.Message], error) {
	body, err := m.buildRequest(input, co)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	rsp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("responses: %w", err)
	}
	if rsp.StatusCode/100 != 2 {
		defer rsp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(rsp.Body, 4096))
		return nil, fmt.Errorf("responses: HTTP %d: %s", rsp.StatusCode, strings.TrimSpace(string(b)))
	}

	debug := debugLLMEnabled()
	if debug {
		debugf("responses request: %s", describeResponsesRequest(body))
	}
	sr, sw := schema.Pipe[*schema.Message](16)
	go func() {
		defer rsp.Body.Close()
		defer sw.Close()
		var nReason, nText, nTool, nMeta int
		var finish string
		err := readResponsesSSE(rsp.Body, func(msg *schema.Message) bool {
			switch {
			case msg.ReasoningContent != "":
				nReason++
			case msg.Content != "":
				nText++
			case len(msg.ToolCalls) > 0:
				nTool++
			case msg.ResponseMeta != nil:
				nMeta++
				finish = msg.ResponseMeta.FinishReason
			}
			return sw.Send(msg, nil) // true = consumer closed
		})
		if debug {
			debugf("responses stream end: reasoning=%d text=%d toolcall=%d completed=%d finish=%q err=%v",
				nReason, nText, nTool, nMeta, finish, err)
		}
		if err != nil {
			sw.Send(nil, err)
		}
	}()
	return sr, nil
}

// describeResponsesRequest summarises a request: item kinds and tool names only.
func describeResponsesRequest(raw []byte) string {
	var r struct {
		Input []struct {
			Role string `json:"role"`
			Type string `json:"type"`
		} `json:"input"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
		Reasoning map[string]any `json:"reasoning"`
	}
	if json.Unmarshal(raw, &r) != nil {
		return "unparsable request body"
	}
	items := make([]string, 0, len(r.Input))
	for _, it := range r.Input {
		if it.Type != "" {
			items = append(items, it.Type)
		} else {
			items = append(items, it.Role)
		}
	}
	names := make([]string, 0, len(r.Tools))
	for _, t := range r.Tools {
		names = append(names, t.Name)
	}
	return fmt.Sprintf("input=[%s] tools=%d[%s] reasoning=%v", strings.Join(items, " | "), len(names), strings.Join(names, ","), r.Reasoning)
}

// ---- request ----

type rItem = map[string]any

func (m *responsesModel) buildRequest(in []*schema.Message, co *model.Options) ([]byte, error) {
	name := m.model
	if co.Model != nil && *co.Model != "" {
		name = *co.Model
	}
	req := map[string]any{
		"model":  name,
		"input":  toResponsesInput(in),
		"stream": true,
		"store":  false,
	}
	if m.effort != "" && m.effort != "none" {
		req["reasoning"] = map[string]any{"effort": m.effort, "summary": m.summary}
	} else if m.effort == "none" {
		req["reasoning"] = map[string]any{"effort": "none"}
	}
	if co.MaxTokens != nil {
		req["max_output_tokens"] = *co.MaxTokens
	}
	if co.Temperature != nil && m.effort == "" {
		req["temperature"] = *co.Temperature
	}
	if len(co.Tools) > 0 {
		tools := make([]rItem, 0, len(co.Tools))
		for _, t := range co.Tools {
			params := any(map[string]any{"type": "object", "properties": map[string]any{}})
			if t.ParamsOneOf != nil {
				js, err := t.ParamsOneOf.ToJSONSchema()
				if err != nil {
					return nil, fmt.Errorf("responses: tool %s schema: %w", t.Name, err)
				}
				if js != nil {
					params = js
				}
			}
			tools = append(tools, rItem{"type": "function", "name": t.Name, "description": t.Desc, "parameters": params, "strict": false})
		}
		req["tools"] = tools
	}
	return json.Marshal(req)
}

func toResponsesInput(in []*schema.Message) []rItem {
	out := make([]rItem, 0, len(in))
	for _, msg := range in {
		if msg == nil {
			continue
		}
		switch msg.Role {
		case schema.System:
			out = append(out, rItem{"role": "system", "content": msg.Content})
		case schema.User:
			out = append(out, rItem{"role": "user", "content": userParts(msg)})
		case schema.Assistant:
			if msg.Content != "" {
				out = append(out, rItem{"role": "assistant", "content": []rItem{{"type": "output_text", "text": msg.Content}}})
			}
			// No "id" on replayed function_call items: with store=false an id would
			// require its paired reasoning item to be replayed too.
			for _, tc := range msg.ToolCalls {
				args := tc.Function.Arguments
				if strings.TrimSpace(args) == "" {
					args = "{}"
				}
				out = append(out, rItem{"type": "function_call", "call_id": tc.ID, "name": tc.Function.Name, "arguments": args})
			}
		case schema.Tool:
			out = append(out, rItem{"type": "function_call_output", "call_id": msg.ToolCallID, "output": msg.Content})
		}
	}
	return out
}

func userParts(msg *schema.Message) []rItem {
	var parts []rItem
	for _, p := range msg.UserInputMultiContent {
		switch p.Type {
		case schema.ChatMessagePartTypeText:
			parts = append(parts, rItem{"type": "input_text", "text": p.Text})
		case schema.ChatMessagePartTypeImageURL:
			if p.Image != nil && p.Image.URL != nil {
				parts = append(parts, rItem{"type": "input_image", "image_url": *p.Image.URL})
			}
		}
	}
	if len(parts) == 0 {
		parts = []rItem{{"type": "input_text", "text": msg.Content}}
	}
	return parts
}

// ---- response stream ----

type rEvent struct {
	Type        string          `json:"type"`
	Delta       string          `json:"delta"`
	OutputIndex int             `json:"output_index"`
	SummaryIdx  int             `json:"summary_index"`
	Item        json.RawMessage `json:"item"`
	Response    *struct {
		Status string `json:"status"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
			InDetails    struct {
				Cached int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutDetails struct {
				Reasoning int `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
	Message string `json:"message"` // type=error
}

type rItemHead struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// readResponsesSSE parses the stream and calls emit per chunk; emit returns
// true to abort (consumer closed). A non-nil error means the stream failed.
func readResponsesSSE(r io.Reader, emit func(*schema.Message) bool) error {
	br := bufio.NewReaderSize(r, 64<<10)
	toolIdx := map[int]int{}     // output_index -> tool call index
	sawArgs := map[int]bool{}    // output_index -> argument deltas seen
	lastSummary := map[int]int{} // output_index -> last summary_index emitted
	nTools := 0
	asst := func(m *schema.Message) *schema.Message { m.Role = schema.Assistant; return m }

	for {
		line, err := br.ReadString('\n')
		if len(line) == 0 && err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("responses: stream ended before response.completed")
			}
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev rEvent
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta":
			if ev.Delta != "" && emit(asst(&schema.Message{Content: ev.Delta})) {
				return nil
			}
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			d := ev.Delta
			if last, seen := lastSummary[ev.OutputIndex]; seen && ev.SummaryIdx != last {
				d = "\n\n" + d // new summary paragraph
			}
			lastSummary[ev.OutputIndex] = ev.SummaryIdx
			if d != "" && emit(asst(&schema.Message{ReasoningContent: d})) {
				return nil
			}
		case "response.output_item.added":
			var h rItemHead
			if json.Unmarshal(ev.Item, &h) == nil && h.Type == "function_call" {
				idx := nTools
				nTools++
				toolIdx[ev.OutputIndex] = idx
				if emit(asst(&schema.Message{ToolCalls: []schema.ToolCall{{
					Index: &idx, ID: h.CallID, Type: "function", Function: schema.FunctionCall{Name: h.Name},
				}}})) {
					return nil
				}
			}
		case "response.function_call_arguments.delta":
			idx, ok := toolIdx[ev.OutputIndex]
			if !ok || ev.Delta == "" {
				continue
			}
			sawArgs[ev.OutputIndex] = true
			if emit(asst(&schema.Message{ToolCalls: []schema.ToolCall{{
				Index: &idx, Function: schema.FunctionCall{Arguments: ev.Delta},
			}}})) {
				return nil
			}
		case "response.output_item.done":
			var h rItemHead
			if json.Unmarshal(ev.Item, &h) == nil && h.Type == "function_call" && !sawArgs[ev.OutputIndex] && h.Arguments != "" {
				if idx, ok := toolIdx[ev.OutputIndex]; ok {
					if emit(asst(&schema.Message{ToolCalls: []schema.ToolCall{{
						Index: &idx, Function: schema.FunctionCall{Arguments: h.Arguments},
					}}})) {
						return nil
					}
				}
			}
		case "response.completed", "response.incomplete":
			finish := "stop"
			if nTools > 0 {
				finish = "tool_calls"
			} else if ev.Response != nil && ev.Response.IncompleteDetails != nil && ev.Response.IncompleteDetails.Reason == "max_output_tokens" {
				finish = "length"
			}
			meta := &schema.ResponseMeta{FinishReason: finish}
			if ev.Response != nil && ev.Response.Usage != nil {
				u := ev.Response.Usage
				meta.Usage = &schema.TokenUsage{
					PromptTokens:            u.InputTokens,
					PromptTokenDetails:      schema.PromptTokenDetails{CachedTokens: u.InDetails.Cached},
					CompletionTokens:        u.OutputTokens,
					TotalTokens:             u.TotalTokens,
					CompletionTokensDetails: schema.CompletionTokensDetails{ReasoningTokens: u.OutDetails.Reasoning},
				}
			}
			emit(asst(&schema.Message{ResponseMeta: meta}))
			return nil
		case "response.failed":
			msg := "response failed"
			if ev.Response != nil && ev.Response.Error != nil && ev.Response.Error.Message != "" {
				msg = ev.Response.Error.Message
			}
			return fmt.Errorf("responses: %s", msg)
		case "error":
			return fmt.Errorf("responses: %s", ev.Message)
		}
	}
}
