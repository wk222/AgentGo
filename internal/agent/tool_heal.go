package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"

	"agentgo/internal/config"
)

// ToolHealEnabled returns true unless AGENTGO_TOOL_HEAL=0.
func ToolHealEnabled() bool {
	return config.ToolHeal.On()
}

// ComposeToolHealMiddleware repairs syntax before execution; it never retries tool side effects.
// Place before governance so approval sees the exact executable arguments.
func ComposeToolHealMiddleware() compose.ToolMiddleware {
	if !ToolHealEnabled() {
		return compose.ToolMiddleware{}
	}
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if input == nil {
					return nil, fmt.Errorf("tool arguments: nil input")
				}
				args, err := prepareToolArguments(input.Arguments)
				if err != nil {
					return nil, err
				}
				// No endpoint has run yet. Never infer retry safety from returned prose.
				return next(ctx, cloneToolInput(input, args))
			}
		},
	}
}

// ADKToolHealMiddleware wraps ADK tool calls with the same pre-execution repair logic as compose middleware.
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
	tc *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return wrapHealInvokableEndpoint(endpoint, tc), nil
}

func (m *ADKToolHealMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	tc *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	return wrapHealInvokableEndpoint(endpoint, tc), nil
}

func wrapHealInvokableEndpoint(endpoint adk.InvokableToolCallEndpoint, tc *adk.ToolContext) adk.InvokableToolCallEndpoint {
	name := ""
	if tc != nil {
		name = tc.Name
	}
	return func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		prepared, err := prepareToolArguments(args)
		if err != nil {
			recordCallTrace(ctx, "", name, TraceStageArgsRepaired, 0, map[string]any{"rejected": err.Error(), "before": args})
			return "", err
		}
		if prepared != args {
			recordCallTrace(ctx, "", name, TraceStageArgsRepaired, 0, map[string]any{"before": args, "after": prepared})
		}
		return endpoint(ctx, prepared, opts...)
	}
}

func prepareToolArguments(args string) (string, error) {
	if len(args) > 512*1024 {
		return "", fmt.Errorf("tool arguments exceed 512 KiB")
	}
	args = RepairToolArgsJSON(args)
	if !json.Valid([]byte(args)) || !strings.HasPrefix(strings.TrimSpace(args), "{") {
		return "", fmt.Errorf("tool arguments must be a valid JSON object; execution was not started")
	}
	return args, nil
}

func cloneToolInput(in *compose.ToolInput, args string) *compose.ToolInput {
	cp := *in
	cp.Arguments = args
	return &cp
}

// RepairToolArgsJSON fixes common LLM tool-argument mistakes (PyBot tool_arg_repair subset).
func RepairToolArgsJSON(raw string) string {
	return restoreWindowsPaths(repairToolArgsSyntax(raw))
}

func repairToolArgsSyntax(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	if json.Valid([]byte(raw)) {
		return raw
	}
	// Unescaped Windows paths and regexes: an invalid escape cannot mean
	// anything else, so double it before trying the other repairs.
	raw = escapeInvalidBackslashes(raw)
	if json.Valid([]byte(raw)) {
		return raw
	}
	// Strip markdown code fences
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
		raw = strings.TrimSpace(raw)
		if json.Valid([]byte(raw)) {
			return raw
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
				return candidate
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
	fixed := repairTrailingCommas(raw)
	if json.Valid([]byte(fixed)) {
		return fixed
	}
	return raw
}

func repairTrailingCommas(raw string) string {
	var b strings.Builder
	quoted, escaped := false, false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quoted {
			b.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
		}
		if c == ',' {
			j := i + 1
			for j < len(raw) && strings.ContainsRune(" \t\r\n", rune(raw[j])) {
				j++
			}
			if j < len(raw) && (raw[j] == '}' || raw[j] == ']') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

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
