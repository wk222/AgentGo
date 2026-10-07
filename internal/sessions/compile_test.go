package sessions

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestCompileProjectedView_ToolCallsPreserved(t *testing.T) {
	msgs := []*schema.Message{
		schema.UserMessage("hello"),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:       "tc1",
			Function: schema.FunctionCall{Name: "get_time", Arguments: `{}`},
		}}),
		schema.ToolMessage("2026", "tc1"),
	}
	view := CompileProjectedView(msgs, CompileOptions{MaxActiveTurns: 10})
	if len(view.ActiveTurn) != 3 {
		t.Fatalf("active turns: %d", len(view.ActiveTurn))
	}
	if len(view.ActiveTurn[1].ToolCalls) != 1 || view.ActiveTurn[1].ToolCalls[0].Function.Name != "get_time" {
		t.Fatalf("tool calls not preserved: %+v", view.ActiveTurn[1])
	}
	out, err := view.Render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, msg := range out {
		if msg.Role == schema.Assistant && len(msg.ToolCalls) == 1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("assistant tool calls lost in render: %+v", out)
	}
}

func TestCompileProjectedView_CompressionHygiene(t *testing.T) {
	var msgs []*schema.Message
	for i := 0; i < 5; i++ {
		msgs = append(msgs, schema.UserMessage("u"), schema.AssistantMessage("a", nil))
	}
	view := CompileProjectedView(msgs, CompileOptions{MaxActiveTurns: 3})
	if len(view.ContextHygiene) == 0 {
		t.Fatal("expected hygiene note when truncating")
	}
}

func TestCompileProjectedView_WorkflowSnapshot(t *testing.T) {
	msgs := []*schema.Message{schema.UserMessage("go")}
	view := CompileProjectedView(msgs, CompileOptions{
		Snapshot: SpineSnapshot{WorkflowContext: []string{"workflow.done: demo"}},
	})
	if len(view.WorkflowContext) != 1 {
		t.Fatalf("workflow: %+v", view.WorkflowContext)
	}
}

func TestCompileProjectedView_AtomicToolPairNormalization(t *testing.T) {
	// Scenario: older turn had assistant tool call (tc_old) and tool response (tc_old)
	// Next turn has assistant tool call (tc_new) and tool response (tc_new)
	// If MaxActiveTurns cuts between assistant (tc_new) and its tool response,
	// or cuts out the parent assistant of an orphan tool, normalization must keep it clean!
	msgs := []*schema.Message{
		schema.UserMessage("query 1"),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:       "tc_old",
			Function: schema.FunctionCall{Name: "fn_old", Arguments: `{}`},
		}}),
		schema.ToolMessage("result_old", "tc_old"),
		schema.UserMessage("query 2"),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:       "tc_new",
			Function: schema.FunctionCall{Name: "fn_new", Arguments: `{}`},
		}}),
		schema.ToolMessage("result_new", "tc_new"),
	}

	// MaxActiveTurns = 2 would naively cut off at the last 2 messages: [Assistant(tc_new), Tool(tc_new)]
	view := CompileProjectedView(msgs, CompileOptions{MaxActiveTurns: 2})
	if len(view.ActiveTurn) != 2 {
		t.Fatalf("expected 2 active messages, got %d", len(view.ActiveTurn))
	}
	if view.ActiveTurn[0].Role != "assistant" || len(view.ActiveTurn[0].ToolCalls) != 1 {
		t.Fatalf("expected paired assistant message, got %+v", view.ActiveTurn[0])
	}
	if view.ActiveTurn[1].Role != "tool" || view.ActiveTurn[1].ToolCallID != "tc_new" {
		t.Fatalf("expected paired tool message, got %+v", view.ActiveTurn[1])
	}

	// Now test what happens if MaxActiveTurns = 1 (would naively only keep the orphan Tool(tc_new))
	// In that case, orphan tool response must NOT be left without assistant caller!
	view1 := CompileProjectedView(msgs, CompileOptions{MaxActiveTurns: 1})
	for _, m := range view1.ActiveTurn {
		if m.Role == "tool" {
			t.Fatalf("orphan tool message should have been dropped: %+v", m)
		}
	}
}
