package agent

import (
	"agentgo/internal/event"
	"context"
	"errors"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
)

func streamTestEvents(terminal error) *adk.AsyncIterator[*adk.AgentEvent] {
	it, g := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	g.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{Role: schema.Tool, Message: schema.ToolMessage("PRIVATE_TOOL_JSON", "call")}}})
	sr, sw := schema.Pipe[*schema.Message](2)
	sw.Send(schema.AssistantMessage("partial answer", nil), nil)
	if terminal != nil {
		sw.Send(nil, terminal)
	}
	sw.Close()
	g.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{Role: schema.Assistant, IsStreaming: true, MessageStream: sr}}})
	g.Close()
	return it
}
func TestTextAndChannelConsumersAgreeOnPartialStreamFailure(t *testing.T) {
	broken := errors.New("stream connection lost")
	for _, channel := range []bool{false, true} {
		var content, shown string
		var err error
		r := &Runner{}
		if channel {
			ch := make(chan *event.Event, 10)
			content, _, _, err = r.drainADKEventsToChannel(context.Background(), streamTestEvents(broken), ch, "run")
			close(ch)
			for e := range ch {
				if e.Response != nil {
					for _, choice := range e.Response.Choices {
						shown += choice.Delta
					}
				}
			}
		} else {
			content, _, _, err = r.drainADKEvents(context.Background(), streamTestEvents(broken), func(s string) { shown += s })
		}
		if !errors.Is(err, broken) || content != "partial answer" || shown != content {
			t.Fatalf("channel=%v content=%q shown=%q err=%v", channel, content, shown, err)
		}
		if strings.Contains(shown, "PRIVATE") {
			t.Fatal("tool result leaked to assistant text")
		}
	}
}
func TestStreamEOFCompletesAndCancelledDrainDoesNot(t *testing.T) {
	r := &Runner{}
	content, _, _, err := r.drainADKEvents(context.Background(), streamTestEvents(nil), nil)
	if err != nil || content != "partial answer" {
		t.Fatal(content, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	it, g := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	g.Close()
	_, _, _, err = r.drainADKEvents(ctx, it, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation reported success", err)
	}
}

// A model attempt the ADK is about to repeat arrives as a stream error marked
// WillRetryError. The drain must skip it and keep only the retried answer.
func TestDrainSkipsAttemptsTheRetryWrapperRepeats(t *testing.T) {
	it, g := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	send := func(chunk string, terminal error) {
		sr, sw := schema.Pipe[*schema.Message](2)
		sw.Send(schema.AssistantMessage(chunk, nil), nil)
		if terminal != nil {
			sw.Send(nil, terminal)
		}
		sw.Close()
		g.Send(&adk.AgentEvent{Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{Role: schema.Assistant, IsStreaming: true, MessageStream: sr}}})
	}
	send("junk from a dropped stream", &adk.WillRetryError{ErrStr: "stream error", RetryAttempt: 0})
	g.Send(&adk.AgentEvent{Err: &adk.WillRetryError{ErrStr: "rejected", RetryAttempt: 1}})
	send("good answer", nil)
	g.Close()

	var shown string
	content, _, _, err := (&Runner{}).drainADKEvents(context.Background(), it, func(s string) { shown += s })
	if err != nil || content != "good answer" {
		t.Fatalf("content=%q err=%v", content, err)
	}
	if !strings.Contains(shown, "good answer") {
		t.Fatalf("retried answer was not streamed: %q", shown)
	}
}
