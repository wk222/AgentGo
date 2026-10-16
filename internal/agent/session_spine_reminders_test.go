package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

func reminderMsg(text string) *schema.Message {
	m := schema.SystemMessage(text)
	m.Extra = map[string]any{adk.MessageExtraKeySystemReminder: true, "__toolsearch_reminder__": true}
	return m
}

func countReminders(msgs []*schema.Message) (n int) {
	for _, m := range msgs {
		if isSystemReminder(m) {
			n++
		}
	}
	return n
}

func TestSplitAndRestoreKeepReminderPosition(t *testing.T) {
	rem := reminderMsg("R")
	in := []*schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage("old"),
		schema.AssistantMessage("a", nil),
		rem,
		schema.UserMessage("q"),
	}
	rest, rs := splitSystemReminders(in)
	if len(rest) != 4 || len(rs) != 1 || rs[0].after != 1 {
		t.Fatalf("split: rest=%d reminders=%d after=%v", len(rest), len(rs), rs)
	}
	// An older message got pruned by the projection: the reminder must still sit
	// directly before the last message.
	rendered := []*schema.Message{schema.SystemMessage("projected"), schema.AssistantMessage("a", nil), schema.UserMessage("q")}
	out := restoreSystemReminders(rendered, rs)
	if len(out) != 4 || out[2] != rem || out[3].Content != "q" || out[0].Content != "projected" {
		t.Fatalf("restore misplaced the reminder: %+v", out)
	}
	if out[2].Extra[adk.MessageExtraKeySystemReminder] != true {
		t.Fatal("reminder marker must survive untouched")
	}
}

// Replays what eino's tool search does on every model call: insert the reminder unless
// a message carrying its marker is already in the state.
func TestSpineKeepsFrameworkReminderMarkerSoItIsInsertedOnce(t *testing.T) {
	spine := NewSessionSpineMiddleware(SessionSpineConfig{})
	ctx := context.Background()

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("q")}}
	var firstSystemLen int
	for call := 0; call < 5; call++ {
		// framework step (before the spine in the real stack)
		if countReminders(state.Messages) == 0 {
			rem := reminderMsg("deferred tools: a b c")
			msgs := append([]*schema.Message{}, state.Messages[:len(state.Messages)-1]...)
			msgs = append(msgs, rem, state.Messages[len(state.Messages)-1])
			state.Messages = msgs
		}
		var err error
		if _, state, err = spine.BeforeModelRewriteState(ctx, state, nil); err != nil {
			t.Fatal(err)
		}

		if n := countReminders(state.Messages); n != 1 {
			t.Fatalf("call %d: %d reminders in the prompt, want exactly 1", call, n)
		}
		if state.Messages[0].Role != schema.System {
			t.Fatalf("call %d: first message must be the projection", call)
		}
		if call == 0 {
			firstSystemLen = len(state.Messages[0].Content)
		} else if got := len(state.Messages[0].Content); got != firstSystemLen {
			t.Fatalf("call %d: system message grew %d -> %d", call, firstSystemLen, got)
		}
		// the model answers with a tool call, the tool answers
		state.Messages = append(state.Messages,
			schema.AssistantMessage("", []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "t", Arguments: "{}"}}}),
			schema.ToolMessage("ok", "c1"))
		state.Messages[len(state.Messages)-2].ToolCalls[0].ID = "c" + string(rune('a'+call))
		state.Messages[len(state.Messages)-1].ToolCallID = state.Messages[len(state.Messages)-2].ToolCalls[0].ID
	}
}
