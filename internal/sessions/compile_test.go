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
