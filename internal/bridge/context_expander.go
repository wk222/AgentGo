package bridge

import (
	"context"

	"agentgo/internal/core"
)

// @mention expansion lives in core (core/mentions.go); this file binds it to
// the live terminal and language-server state of this process.

func (s *AppService) expandUserMentions(_ context.Context, userText string) string {
	src := core.MentionSources{Root: s.rt.WorkspaceRoot()}
	if ts := s.rt.TerminalService(); ts != nil {
		src.Terminal = func() (string, string, bool) {
			terms := ts.ListTerminals()
			if len(terms) == 0 {
				return "", "", false
			}
			active := terms[len(terms)-1]
			return active.Title, ts.PollTerminal(active.ID), true
		}
	}
	if tb := s.getCodetools(); tb != nil {
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
