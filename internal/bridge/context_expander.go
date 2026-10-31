package bridge

import (
	"context"

	"agentgo/internal/coderuntime"
	"agentgo/internal/core"
)

// @mention expansion lives in core (core/mentions.go); this file binds it to
// the live terminal and language-server state of this process.

// expandUserInput turns what the user typed into what the model receives:
// a leading slash command ("/review ...") becomes its full instructions first,
// then @mentions anywhere in the text (including in the command's argument)
// are replaced by their context blocks.
func (s *AppService) expandUserInput(ctx context.Context, userText string) string {
	return s.rt.expandUserInput(ctx, userText)
}

func (rt *Runtime) expandUserInput(ctx context.Context, userText string) string {
	return rt.expandUserMentions(ctx, core.ExpandCommand(userText))
}

func (s *AppService) expandUserMentions(ctx context.Context, userText string) string {
	return s.rt.expandUserMentions(ctx, userText)
}

func (rt *Runtime) codeToolbox() coderuntime.Toolbox {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.codetools
}

func (rt *Runtime) expandUserMentions(_ context.Context, userText string) string {
	src := core.MentionSources{Root: rt.WorkspaceRoot()}
	if ts := rt.TerminalService(); ts != nil {
		src.Terminal = func() (string, string, bool) {
			terms := ts.ListTerminals()
			if len(terms) == 0 {
				return "", "", false
			}
			active := terms[len(terms)-1]
			return active.Title, ts.PollTerminal(active.ID), true
		}
	}
	if tb := rt.codeToolbox(); tb != nil {
		src.Diagnostics = func() (map[string]int, func(string) any, bool) {
			counts := map[string]int{}
			for name, st := range tb.LSPStates() {
				counts[name] = st.DiagnosticCount
			}
			return counts, func(name string) any { return tb.LSPDiagnostics(name) }, true
		}
	}
	return core.ExpandMentions(userText, src)
}
