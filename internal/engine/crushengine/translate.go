package crushengine

import (
	"encoding/json"
	"strings"

	"agentgo/internal/engine"
)

// translator converts Crush SSE payloads (message *snapshots*, file versions)
// into unified engine events (deltas, tool calls/results, file changes).
// One translator serves one run; it is not safe for concurrent use.
type translator struct {
	sessionID string
	em        engine.Emitter

	text      map[string]string // messageID -> assistant text already emitted
	reasoning map[string]string // messageID -> reasoning already emitted
	calls     map[string]bool   // toolCallID -> tool_call emitted
	results   map[string]bool   // toolCallID -> tool_result emitted
	files     map[string]string // path -> last known content
}

func newTranslator(sessionID string, em engine.Emitter) *translator {
	return &translator{
		sessionID: sessionID, em: em,
		text: map[string]string{}, reasoning: map[string]string{},
		calls: map[string]bool{}, results: map[string]bool{}, files: map[string]string{},
	}
}

// suffixDelta returns the newly appended text. When the snapshot is not an
// extension of what was emitted (rewrite), nothing is emitted but the cursor
// still advances so later appends are diffed correctly.
func suffixDelta(seen *string, cur string) string {
	prev := *seen
	*seen = cur
	if strings.HasPrefix(cur, prev) {
		return cur[len(prev):]
	}
	return ""
}

func (t *translator) onMessage(m wireMessage) {
	if m.SessionID != t.sessionID {
		return
	}
	switch m.Role {
	case "assistant":
		var text, think strings.Builder
		var calls []toolCallData
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				var d textData
				if json.Unmarshal(p.Data, &d) == nil && !d.Hidden {
					text.WriteString(d.Text)
				}
			case "reasoning":
				var d reasoningData
				if json.Unmarshal(p.Data, &d) == nil {
					think.WriteString(d.Thinking)
				}
			case "tool_call":
				var d toolCallData
				// Emit only once the input is complete (Finished), so the
				// frontend never sees half-streamed arguments.
				if json.Unmarshal(p.Data, &d) == nil && d.Finished && d.ID != "" && !t.calls[d.ID] {
					t.calls[d.ID] = true
					calls = append(calls, d)
				}
			}
		}
		if s := t.reasoning[m.ID]; think.Len() > 0 || s != "" {
			if d := suffixDelta(&s, think.String()); d != "" {
				t.em.Reasoning(d)
			}
			t.reasoning[m.ID] = s
		}
		if s := t.text[m.ID]; text.Len() > 0 || s != "" {
			if d := suffixDelta(&s, text.String()); d != "" {
				t.em.Token(d)
			}
			t.text[m.ID] = s
		}
		// Calls come after the narration that precedes them in the same message.
		for _, d := range calls {
			t.em.ToolCall(d.ID, d.Name, d.Input)
		}
	case "tool":
		for _, p := range m.Parts {
			if p.Type != "tool_result" {
				continue
			}
			var d toolResultData
			if json.Unmarshal(p.Data, &d) != nil || d.ToolCallID == "" || t.results[d.ToolCallID] {
				continue
			}
			t.results[d.ToolCallID] = true
			t.em.ToolResult(d.ToolCallID, d.Name, d.Content, d.IsError)
		}
	}
}

// onFile emits a file_change when a new version of a tracked file appears.
// Crush stores the original as version 0, then one version per edit.
func (t *translator) onFile(f wireFile) {
	if f.SessionID != t.sessionID || f.Path == "" {
		return
	}
	prev, seen := t.files[f.Path]
	t.files[f.Path] = f.Content
	switch {
	case seen:
		if prev != f.Content {
			t.em.FileChange(f.Path, prev, f.Content, "")
		}
	case f.Version == 0:
		// baseline snapshot only
	default:
		t.em.FileChange(f.Path, "", f.Content, "") // created
	}
}
