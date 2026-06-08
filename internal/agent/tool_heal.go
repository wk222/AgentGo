package agent

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
)

const maxToolHealAttempts = 2

// ToolHealEnabled returns true unless AGENTGO_TOOL_HEAL=0.
func ToolHealEnabled() bool {
	return strings.TrimSpace(os.Getenv("AGENTGO_TOOL_HEAL")) != "0"
}

// ComposeToolHealMiddleware repairs malformed tool JSON and retries transient arg/parse failures.
// Place after governance middleware, before SafeTool (handlers).
func ComposeToolHealMiddleware() compose.ToolMiddleware {
	if !ToolHealEnabled() {
		return compose.ToolMiddleware{}
	}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if input == nil {
					return next(ctx, input)
				}
				args := strings.TrimSpace(input.Arguments)
				if args == "" {
					args = "{}"
				}
				repaired := RepairToolArgsJSON(args)
				if repaired != args {
					input = cloneToolInput(input, repaired)
				}
				out, err := next(ctx, input)
				if err == nil && !looksLikeToolArgFailure(out) {
					return out, nil
				}
				for attempt := 0; attempt < maxToolHealAttempts; attempt++ {
					nextArgs := RepairToolArgsJSON(input.Arguments)
					if nextArgs == input.Arguments {
						nextArgs = aggressiveRepairToolArgs(input.Arguments)
					}
					if nextArgs == input.Arguments {
						break
					}
					input = cloneToolInput(input, nextArgs)
					out, err = next(ctx, input)
					if err == nil && !looksLikeToolArgFailure(out) {
						return out, nil
					}
				}
				if err != nil {
					return nil, err
				}
				return out, nil
			}
		},
	}
}

// ADKToolHealMiddleware wraps ADK tool calls with the same repair/retry logic as compose middleware.
type ADKToolHealMiddleware struct {
	adk.BaseChatModelAgentMiddleware
}

func NewADKToolHealMiddleware() adk.ChatModelAgentMiddleware {
	if !ToolHealEnabled() {
		return nil
	}
	return &ADKToolHealMiddleware{}
}

// NewTypedADKToolHealMiddleware is the AgenticMessage variant.
func NewTypedADKToolHealMiddleware[M adk.MessageType]() adk.TypedChatModelAgentMiddleware[M] {
	if !ToolHealEnabled() {
		return nil
	}
	return &typedADKToolHealMiddleware[M]{}
}

type typedADKToolHealMiddleware[M adk.MessageType] struct {
	adk.TypedBaseChatModelAgentMiddleware[M]
}

func (m *typedADKToolHealMiddleware[M]) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return wrapHealInvokableEndpoint(endpoint), nil
}

func (m *ADKToolHealMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	_ *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return wrapHealInvokableEndpoint(endpoint), nil
}

func wrapHealInvokableEndpoint(endpoint adk.InvokableToolCallEndpoint) adk.InvokableToolCallEndpoint {
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		args = strings.TrimSpace(args)
		if args == "" {
			args = "{}"
		}
		repaired := RepairToolArgsJSON(args)
		if repaired != args {
			args = repaired
		}
		result, err := endpoint(ctx, args, opts...)
		if err == nil && !looksLikeToolArgFailureString(result) {
			return result, nil
		}
		if err != nil && !isToolArgError(err) {
			return "", err
		}
		for attempt := 0; attempt < maxToolHealAttempts; attempt++ {
			nextArgs := RepairToolArgsJSON(args)
			if nextArgs == args {
				nextArgs = aggressiveRepairToolArgs(args)
			}
			if nextArgs == args {
				break
			}
			args = nextArgs
			result, err = endpoint(ctx, args, opts...)
			if err == nil && !looksLikeToolArgFailureString(result) {
				return result, nil
			}
			if err != nil && !isToolArgError(err) {
				return "", err
			}
		}
		if err != nil {
			return "", err
		}
		return result, nil
	}
}

func cloneToolInput(in *compose.ToolInput, args string) *compose.ToolInput {
	cp := *in
	cp.Arguments = args
	return &cp
}

func looksLikeToolArgFailure(out *compose.ToolOutput) bool {
	if out == nil {
		return false
	}
	return looksLikeToolArgFailureString(out.Result)
}

func looksLikeToolArgFailureString(s string) bool {
	lo := strings.ToLower(s)
	return strings.Contains(lo, "invalid character") ||
		strings.Contains(lo, "cannot unmarshal") ||
		strings.Contains(lo, "json:") ||
		strings.Contains(lo, "unexpected end of json") ||
		strings.Contains(lo, "validation error") ||
		strings.Contains(lo, "invalid type") ||
		strings.Contains(lo, "required field")
}

func isToolArgError(err error) bool {
	if err == nil {
		return false
	}
	return looksLikeToolArgFailureString(err.Error())
}

// RepairToolArgsJSON fixes common LLM tool-argument mistakes (PyBot tool_arg_repair subset).
func RepairToolArgsJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	if json.Valid([]byte(raw)) {
		return normalizeToolArgsObject(raw)
	}
	// Strip markdown code fences
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
		raw = strings.TrimSpace(raw)
		if json.Valid([]byte(raw)) {
			return normalizeToolArgsObject(raw)
		}
	}
	// Double-encoded JSON string
	if (strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`)) || (strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'")) {
		var inner string
		if json.Unmarshal([]byte(raw), &inner) == nil && json.Valid([]byte(inner)) {
			return normalizeToolArgsObject(inner)
		}
	}
	// Extract first {...} blob
	if i := strings.Index(raw, "{"); i >= 0 {
		if j := strings.LastIndex(raw, "}"); j > i {
			candidate := raw[i : j+1]
			if json.Valid([]byte(candidate)) {
				return normalizeToolArgsObject(candidate)
			}
		}
	}
	// Array with one object -> unwrap
	if strings.HasPrefix(raw, "[") {
		var arr []json.RawMessage
		if json.Unmarshal([]byte(raw), &arr) == nil && len(arr) == 1 {
			return strings.TrimSpace(string(arr[0]))
		}
	}
	// Trailing commas (invalid JSON)
	fixed := trailingCommaRE.ReplaceAllString(raw, "$1")
	if json.Valid([]byte(fixed)) {
		return normalizeToolArgsObject(fixed)
	}
	return raw
}

var trailingCommaRE = regexp.MustCompile(`,(\s*[}\]])`)

func aggressiveRepairToolArgs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return `{"input":""}`
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	// Key=value lines -> object
	if strings.Contains(raw, "=") && !strings.Contains(raw, "{") {
		m := map[string]string{}
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				m[strings.TrimSpace(parts[0])] = strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			}
		}
		if len(m) > 0 {
			b, _ := json.Marshal(m)
			return string(b)
		}
	}
	// Plain text -> wrap as input
	b, _ := json.Marshal(map[string]string{"input": raw})
	return string(b)
}

func normalizeToolArgsObject(raw string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &m) != nil {
		return raw
	}
	// Unwrap {"arguments": {...}} wrapper from some models
	if len(m) == 1 {
		if inner, ok := m["arguments"]; ok {
			inner = json.RawMessage(strings.TrimSpace(string(inner)))
			if json.Valid(inner) {
				return string(inner)
			}
		}
	}
	// Coerce stringified JSON values for common keys
	for _, key := range []string{"payload_json", "args_json", "nodes_json", "data_json"} {
		v, ok := m[key]
		if !ok {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil && json.Valid([]byte(s)) {
			m[key] = json.RawMessage(s)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return string(b)
}

// AppendComposeToolMiddlewares appends heal (and optional governance) to tool node config.
func AppendComposeToolMiddlewares(base []compose.ToolMiddleware, extra ...compose.ToolMiddleware) []compose.ToolMiddleware {
	out := make([]compose.ToolMiddleware, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}
