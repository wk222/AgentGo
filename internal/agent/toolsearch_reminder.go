package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
)

// DeepSeek models answer a bare list of deferred tool names by trying to call
// them. Their calls use a text syntax (fullwidth-bar DSML tags); a name that is
// not in the tool list makes a relay's parser drop the call, so the turn ends
// with the tail of that syntax or a run of invented names as the "answer".
// Measured against a DeepSeek V4 Flash relay with a 12-tool request:
// the default reminder gave 0/8 usable replies, the same request without it 7/8,
// and the keyword hint below 5/8. Rewording the list kept it at 0.

type modelNameKey struct{}

// withModelName records the configured model ID for middleware built later in
// the same turn.
func withModelName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, modelNameKey{}, strings.TrimSpace(name))
}

func modelNameFrom(ctx context.Context) string {
	name, _ := ctx.Value(modelNameKey{}).(string)
	return name
}

// isDeepSeekFamily matches the IDs a relay uses for DeepSeek models: "ds",
// "deepseek", "deepseek-v4-flash", "moma/dsv4", ...
func isDeepSeekFamily(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:]
	}
	return m == "ds" || strings.HasPrefix(m, "ds-") || strings.HasPrefix(m, "dsv") || strings.Contains(m, "deepseek")
}

const keywordReminder = "More tools exist beyond the ones in your tool list (workspace files, memory, web search, workflows, pipelines, MCP servers, agent packs). " +
	"Their schemas are not loaded: when a task needs one, call tool_search with a short keyword query (for example \"web search\" or \"workspace file\") and it returns the tool definitions. " +
	"Do not call a tool that is not in your tool list. If the request needs no tool, answer directly."

// toolSearchConfig builds the middleware config. Models that mishandle a name
// list get no mid-conversation reminder at all; the keyword hint goes into the
// system instruction instead (see toolSearchInstructionHint).
//
// A reminder sitting after the user message makes a DeepSeek relay open its
// reply by repeating tool-usage sentences. Measured with the same 22-tool
// request, 8 runs each: trailing reminder 2 leaks, no reminder 0, reminder
// folded into the system message 0 (empty replies were relay errors, not text).
func toolSearchConfig(ctx context.Context, cfg *toolsearch.Config) *toolsearch.Config {
	if !isDeepSeekFamily(modelNameFrom(ctx)) {
		return cfg
	}
	cfg.CustomFormatReminder = func(context.Context, *toolsearch.FormatReminderInput) (*toolsearch.FormatReminderOutput, error) {
		return &toolsearch.FormatReminderOutput{Reminder: ""}, nil
	}
	return cfg
}

// toolSearchInstructionHint is the text appended to the system instruction for
// models whose reminder was dropped by toolSearchConfig.
func toolSearchInstructionHint(model string) string {
	if !isDeepSeekFamily(model) {
		return ""
	}
	return " " + keywordReminder
}
