package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

// UsedTools must reflect observed tool events: refusal recovery gates on a
// turn with no tool use, so a hardcoded true would silently disable it.
func TestToolUsageTrackerObservesDrainEvents(t *testing.T) {
	r := &Runner{}

	// Text-only run: no tools used.
	ctx, tracker := withToolUsageTracker(context.Background())
	it, g := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	g.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant, Message: schema.AssistantMessage("抱歉，我无法协助。", nil),
	}}})
	g.Close()
	if _, _, _, err := r.drainADKEvents(ctx, it, nil); err != nil {
		t.Fatal(err)
	}
	if tracker.used.Load() {
		t.Fatal("text-only run must not report tool use")
	}

	// Tool-call run: assistant emits a tool call, then a tool result arrives.
	ctx, tracker = withToolUsageTracker(context.Background())
	it, g = adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	g.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant,
		Message: schema.AssistantMessage("", []schema.ToolCall{{
			ID: "c1", Type: "function",
			Function: schema.FunctionCall{Name: "sb_list", Arguments: "{}"},
		}}),
	}}})
	g.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Tool, Message: schema.ToolMessage("[]", "c1"),
	}}})
	g.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
		Role: schema.Assistant, Message: schema.AssistantMessage("done", nil),
	}}})
	g.Close()
	if _, _, _, err := r.drainADKEvents(ctx, it, nil); err != nil {
		t.Fatal(err)
	}
	if !tracker.used.Load() {
		t.Fatal("tool-call run must report tool use")
	}
}
