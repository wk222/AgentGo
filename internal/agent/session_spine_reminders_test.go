package agent

import (
	"context"
	"fmt"
	"testing"

	"agentgo/internal/sessions"

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

// toolPairsIntact reports the first assistant tool call whose results do not follow it
// directly. Providers answer such a prompt with HTTP 400 ("tool_calls must be followed
// by tool messages"), which fails the whole run.
func toolPairsIntact(msgs []*schema.Message) error {
	for i, m := range msgs {
		if m == nil || m.Role != schema.Assistant {
			continue
		}
		for j, tc := range m.ToolCalls {
			k := i + 1 + j
			if k >= len(msgs) || msgs[k].Role != schema.Tool || msgs[k].ToolCallID != tc.ID {
				return fmt.Errorf("tool call %q at message %d is not followed by its result", tc.ID, i)
			}
		}
	}
	return nil
}

// The projection puts its runtime-context message after the conversation. A reminder is
// restored by counting trailing messages, so that extra tail message must not push it
// into the middle of a tool call and its result.
func TestSpineReminderNeverSplitsToolCallFromItsResult(t *testing.T) {
	spine := NewSessionSpineMiddleware(SessionSpineConfig{})
	ctx := sessions.WithSpineSnapshot(context.Background(), sessions.SpineSnapshot{WorkspaceRecent: []string{"recent: a.py"}})

	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.SystemMessage("BASE"), schema.UserMessage("q")}}
	for call := 0; call < 4; call++ {
		// Tool search inserts its reminder once, right after the latest user message.
		if countReminders(state.Messages) == 0 {
			state.Messages = append(state.Messages, reminderMsg("deferred tools: a b c"))
		}
		var err error
		if _, state, err = spine.BeforeModelRewriteState(ctx, state, nil); err != nil {
			t.Fatal(err)
		}
		if n := countReminders(state.Messages); n != 1 {
			t.Fatalf("call %d: %d reminders in the prompt, want exactly 1", call, n)
		}
		if err := toolPairsIntact(state.Messages); err != nil {
			t.Fatalf("call %d: %v\n%s", call, err, describeRoles(state.Messages))
		}
		id := fmt.Sprintf("call_%d", call)
		state.Messages = append(state.Messages,
			schema.AssistantMessage("", []schema.ToolCall{{ID: id, Function: schema.FunctionCall{Name: "t", Arguments: "{}"}}}),
			schema.ToolMessage("ok", id))
	}
}

// Whatever shifts the count (pruning, a message the projection added), the reminder
// must land in front of the whole tool call group instead of inside it.
func TestRestoreReminderStepsBackOverToolResults(t *testing.T) {
	rem := reminderMsg("R")
	call := schema.AssistantMessage("", []schema.ToolCall{
		{ID: "a", Function: schema.FunctionCall{Name: "t", Arguments: "{}"}},
		{ID: "b", Function: schema.FunctionCall{Name: "t", Arguments: "{}"}},
	})
	rendered := []*schema.Message{
		schema.SystemMessage("projected"), schema.UserMessage("q"), call,
		schema.ToolMessage("ra", "a"), schema.ToolMessage("rb", "b"), schema.UserMessage("next"),
	}
	for _, after := range []int{1, 2, 3} {
		out := restoreSystemReminders(append([]*schema.Message(nil), rendered...), []spineReminder{{msg: rem, after: after}})
		if err := toolPairsIntact(out); err != nil {
			t.Fatalf("after=%d: %v\n%s", after, err, describeRoles(out))
		}
		if countReminders(out) != 1 {
			t.Fatalf("after=%d: reminder lost or duplicated\n%s", after, describeRoles(out))
		}
	}
}

func describeRoles(msgs []*schema.Message) string {
	out := ""
	for i, m := range msgs {
		kind := ""
		switch {
		case isSystemReminder(m):
			kind = " reminder"
		case sessions.ContextKind(m) != "":
			kind = " ctx:" + sessions.ContextKind(m)
		}
		out += fmt.Sprintf("  [%d] %s%s calls=%d\n", i, m.Role, kind, len(m.ToolCalls))
	}
	return out
}
