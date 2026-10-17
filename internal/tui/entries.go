package tui

import (
	"encoding/json"
	"strings"

	"agentgo/internal/cli"
	"agentgo/internal/engine"
)

type kind int

const (
	kUser kind = iota
	kAssistant
	kReasoning
	kTool
	kDiff
	kNotice
	kErr
)

// entry is one item of the conversation timeline. Streaming text grows the
// last entry in place; everything else is appended.
type entry struct {
	kind kind
	text string // user/assistant/reasoning/notice/err text; tool: one-line call summary

	// tool
	id     string
	name   string
	result string
	isErr  bool
	done   bool

	// diff
	path string
	diff string

	closed bool // assistant/reasoning: no more tokens will arrive

	md      string // cached markdown rendering of a closed assistant entry
	mdWidth int
}

// timeline is the ordered list of entries plus the little state needed to fold
// engine events into it.
type timeline struct {
	entries  []entry
	thinking bool
	streamed bool // assistant tokens arrived this turn
	rev      int  // bumped on every change, so the view re-renders only when needed
}

func (t *timeline) add(e entry) {
	t.rev++
	t.closeStreams()
	t.entries = append(t.entries, e)
}

func (t *timeline) closeStreams() {
	if n := len(t.entries); n > 0 {
		last := &t.entries[n-1]
		if (last.kind == kAssistant || last.kind == kReasoning) && !last.closed {
			last.closed = true
			t.rev++ // a closed reply is drawn as markdown, so it must re-render
		}
	}
}

// appendText grows the trailing stream of kind k, or starts a new one.
func (t *timeline) appendText(k kind, s string) {
	if s == "" {
		return
	}
	if n := len(t.entries); n > 0 {
		last := &t.entries[n-1]
		if last.kind == k && !last.closed {
			last.text += s
			last.md = ""
			t.rev++
			return
		}
	}
	t.add(entry{kind: k, text: s})
}

// apply folds one engine event into the timeline.
func (t *timeline) apply(e engine.Event) {
	str := func(k string) string { s, _ := e.Payload[k].(string); return s }
	switch e.Type {
	case engine.EventToken:
		if d := str("delta"); d != "" {
			t.streamed = true
			t.appendText(kAssistant, d)
		}
	case engine.EventReasoning:
		if t.thinking {
			t.appendText(kReasoning, str("delta"))
		}
	case engine.EventToolCall:
		t.add(entry{kind: kTool, id: str("id"), name: str("name"), text: oneLine(str("arguments"), 200)})
	case engine.EventToolResult:
		isErr, _ := e.Payload["is_error"].(bool)
		id := str("id")
		for i := len(t.entries) - 1; i >= 0; i-- {
			en := &t.entries[i]
			if en.kind == kTool && !en.done && (id == "" || en.id == id) {
				en.result, en.isErr, en.done = str("output"), isErr, true
				t.rev++
				return
			}
		}
		t.add(entry{kind: kTool, name: str("name"), result: str("output"), isErr: isErr, done: true})
	case engine.EventFileChange:
		if d := str("unified_diff"); strings.TrimSpace(d) != "" {
			t.add(entry{kind: kDiff, path: str("path"), diff: d})
		}
	case engine.EventError:
		t.add(entry{kind: kErr, text: str("message")})
	}
}

func (t *timeline) notice(s string) { t.add(entry{kind: kNotice, text: s}) }
func (t *timeline) fail(s string)   { t.add(entry{kind: kErr, text: s}) }
func (t *timeline) user(s string)   { t.add(entry{kind: kUser, text: s}) }

// assistantText records a finished assistant message the stream did not carry.
func (t *timeline) assistantText(s string) {
	if strings.TrimSpace(s) != "" {
		t.add(entry{kind: kAssistant, text: s, closed: true})
	}
}

// load replaces the timeline with a saved conversation.
func (t *timeline) load(msgs []cli.Message) {
	t.entries = nil
	t.rev++
	for _, m := range msgs {
		switch {
		case m.Type == "approval" || m.Type == "question":
			continue
		case m.Role == "user":
			t.user(m.Content)
		case m.Role == "assistant":
			t.assistantText(m.Content)
		}
	}
	t.closeStreams()
}

// oneLine collapses s to a single line of at most n runes.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	rs := []rune(s)
	if len(rs) > n {
		return string(rs[:n]) + "…"
	}
	return s
}

// prettyJSON indents a JSON object for display; non-JSON comes back unchanged.
func prettyJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(b)
}
