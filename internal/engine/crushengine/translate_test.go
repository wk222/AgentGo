package crushengine

import (
	"encoding/json"
	"testing"

	"agentgo/internal/engine"
)

// recEm records emitter calls as compact strings.
type recEm struct{ got []string }

func (r *recEm) add(s string)                       { r.got = append(r.got, s) }
func (r *recEm) Status(s string, _ map[string]any)  { r.add("status:" + s) }
func (r *recEm) Token(d string)                     { r.add("tok:" + d) }
func (r *recEm) Reasoning(d string)                 { r.add("think:" + d) }
func (r *recEm) ToolCall(id, n, a string)           { r.add("call:" + id + ":" + n + ":" + a) }
func (r *recEm) ToolResult(id, n, o string, e bool) { r.add("result:" + id + ":" + o) }
func (r *recEm) FileChange(p, b, a, _ string)       { r.add("file:" + p + ":" + b + "->" + a) }
func (r *recEm) ApprovalRequest(id, t, _, _ string) { r.add("approval:" + id + ":" + t) }
func (r *recEm) Error(m string)                     { r.add("err:" + m) }

var _ engine.Emitter = (*recEm)(nil)

func part(typ string, data any) wirePart {
	b, _ := json.Marshal(data)
	return wirePart{Type: typ, Data: b}
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q\nwant %q", got, want)
		}
	}
}

func TestTextAndReasoningAreDiffedIntoDeltas(t *testing.T) {
	em := &recEm{}
	tr := newTranslator("s1", em)
	snap := func(think, text string) wireMessage {
		return wireMessage{ID: "m1", Role: "assistant", SessionID: "s1", Parts: []wirePart{
			part("reasoning", reasoningData{Thinking: think}),
			part("text", textData{Text: text}),
		}}
	}
	tr.onMessage(snap("", ""))        // nothing yet
	tr.onMessage(snap("hm", ""))      // reasoning starts
	tr.onMessage(snap("hmm", "Hel"))  // both grow
	tr.onMessage(snap("hmm", "Hello")) // text grows
	tr.onMessage(snap("hmm", "Hello")) // duplicate snapshot -> no event
	expect(t, em.got, "think:hm", "think:m", "tok:Hel", "tok:lo")
}

func TestHiddenTextAndOtherSessionsIgnored(t *testing.T) {
	em := &recEm{}
	tr := newTranslator("s1", em)
	tr.onMessage(wireMessage{ID: "m", Role: "assistant", SessionID: "other",
		Parts: []wirePart{part("text", textData{Text: "x"})}})
	tr.onMessage(wireMessage{ID: "m2", Role: "assistant", SessionID: "s1",
		Parts: []wirePart{part("text", textData{Text: "secret", Hidden: true})}})
	tr.onMessage(wireMessage{ID: "u", Role: "user", SessionID: "s1",
		Parts: []wirePart{part("text", textData{Text: "prompt"})}})
	expect(t, em.got)
}

func TestToolCallEmittedOnceWhenFinishedThenResult(t *testing.T) {
	em := &recEm{}
	tr := newTranslator("s1", em)
	call := func(input string, fin bool) wireMessage {
		return wireMessage{ID: "m1", Role: "assistant", SessionID: "s1", Parts: []wirePart{
			part("tool_call", toolCallData{ID: "c1", Name: "edit", Input: input, Finished: fin}),
		}}
	}
	tr.onMessage(call(`{"fi`, false)) // still streaming args
	tr.onMessage(call(`{"file":"a.go"}`, true))
	tr.onMessage(call(`{"file":"a.go"}`, true)) // repeat snapshot
	res := wireMessage{ID: "m2", Role: "tool", SessionID: "s1", Parts: []wirePart{
		part("tool_result", toolResultData{ToolCallID: "c1", Name: "edit", Content: "ok"}),
	}}
	tr.onMessage(res)
	tr.onMessage(res)
	expect(t, em.got, `call:c1:edit:{"file":"a.go"}`, "result:c1:ok")
}

func TestFileVersions(t *testing.T) {
	em := &recEm{}
	tr := newTranslator("s1", em)
	tr.onFile(wireFile{SessionID: "s1", Path: "a.go", Content: "v0", Version: 0}) // baseline
	tr.onFile(wireFile{SessionID: "s1", Path: "a.go", Content: "v1", Version: 1})
	tr.onFile(wireFile{SessionID: "s1", Path: "a.go", Content: "v1", Version: 2}) // no change
	tr.onFile(wireFile{SessionID: "s1", Path: "new.go", Content: "n", Version: 1}) // created
	tr.onFile(wireFile{SessionID: "x", Path: "z.go", Content: "n", Version: 1})    // other session
	expect(t, em.got, "file:a.go:v0->v1", "file:new.go:->n")
}
