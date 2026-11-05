package agent

import (
	"context"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
)

// A tool kept behind tool_search is only found when the model decides to search.
// For "look at my other conversation" or "let Claude Code do this" the model's prior
// is "I cannot", and it answers that without searching (measured: qwen3.7-max, both
// requests, no tool_search call at all). These capabilities are named in the
// system instruction instead, only when the tool is really offered in this mode.
var deferredCapabilities = []struct{ tool, what string }{
	{"session_message", "session_message (read, or send a message to, another conversation of this user)"},
	{"conversation_create", "conversation_create (open a new conversation, optionally as one of the experts that list_experts names)"},
	{"external_agent", "external_agent (hand a task to an installed coding agent such as Claude Code, Codex or Gemini CLI)"},
}

// deferredCapabilityHint is the sentence appended to the system instruction, or ""
// when none of the capabilities is available.
func deferredCapabilityHint(ctx context.Context, dynamic []einotool.BaseTool) string {
	have := make(map[string]bool, len(dynamic))
	for _, t := range dynamic {
		if info, err := t.Info(ctx); err == nil && info != nil {
			have[info.Name] = true
		}
	}
	var parts []string
	for _, c := range deferredCapabilities {
		if have[c.tool] {
			parts = append(parts, c.what)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " These exist but sit behind tool_search; search for them instead of saying you cannot: " + strings.Join(parts, "; ") + "."
}
