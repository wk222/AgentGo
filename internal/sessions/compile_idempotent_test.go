package sessions

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// A ReAct run compiles the same state before every model call and keeps the rewritten
// messages for the next call; other middlewares then append their own text to the
// rendered system message. The compile must not feed its own output back into itself:
// the baseline showed the system prompt growing ~2k chars per call, nesting
// <system_rules> wrappers and changing the prefix at byte 88 of every request.
func TestCompileProjectedViewIsIdempotentAcrossModelCalls(t *testing.T) {
	render := func(msgs []*schema.Message) []*schema.Message {
		out, err := CompileProjectedView(msgs, CompileOptions{WorkspaceRules: "WS-RULES"}).Render(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	state := []*schema.Message{schema.SystemMessage("BASE PROMPT"), schema.UserMessage("question")}
	first := render(state)
	firstSystem := first[0].Content
	if strings.Count(firstSystem, "<system_rules>") != 1 || !strings.Contains(firstSystem, "BASE PROMPT") {
		t.Fatalf("unexpected first render: %q", firstSystem)
	}

	state = first
	for i := 0; i < 6; i++ {
		// what other middlewares do after the projection, in place, on every call
		state[0].Content += "\n\n[mode hint]"
		state = append(state,
			schema.AssistantMessage("", []schema.ToolCall{{ID: "c", Function: schema.FunctionCall{Name: "t", Arguments: "{}"}}}),
			schema.ToolMessage("result", "c"))
		state = render(state)

		if got := state[0].Content; got != firstSystem {
			t.Fatalf("call %d: system message changed.\nfirst: %q\n now : %q", i+2, firstSystem, got)
		}
	}
}

func TestCompileProjectedViewKeepsMultipleSystemMessagesStable(t *testing.T) {
	msgs := []*schema.Message{schema.SystemMessage("RULE A"), schema.SystemMessage("RULE B"), schema.UserMessage("q")}
	r1, _ := CompileProjectedView(msgs, CompileOptions{}).Render(context.Background())
	r2, _ := CompileProjectedView(r1, CompileOptions{}).Render(context.Background())
	r3, _ := CompileProjectedView(r2, CompileOptions{}).Render(context.Background())
	if r1[0].Content != r2[0].Content || r2[0].Content != r3[0].Content {
		t.Fatalf("system message drifted:\n1: %q\n2: %q\n3: %q", r1[0].Content, r2[0].Content, r3[0].Content)
	}
	if !strings.Contains(r1[0].Content, "RULE A") || !strings.Contains(r1[0].Content, "RULE B") {
		t.Fatalf("a system message was lost: %q", r1[0].Content)
	}
}

// A system message that was NOT produced by Render (no marker) is raw input, as before.
func TestCompileProjectedViewTreatsUnmarkedSystemAsRaw(t *testing.T) {
	r, _ := CompileProjectedView([]*schema.Message{schema.SystemMessage("RAW"), schema.UserMessage("q")}, CompileOptions{}).Render(context.Background())
	if !strings.Contains(r[0].Content, "RAW") {
		t.Fatalf("raw system content missing: %q", r[0].Content)
	}
}
