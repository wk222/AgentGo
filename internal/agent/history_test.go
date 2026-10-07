package agent

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestRunMessagesWithoutHistoryIsSingleUserMessage(t *testing.T) {
	got := runMessages(context.Background(), "hello")
	if len(got) != 1 || got[0].Role != schema.User || got[0].Content != "hello" {
		t.Fatalf("messages = %+v", got)
	}
}

func TestRunMessagesAppendsInputAfterHistory(t *testing.T) {
	hist := []*schema.Message{schema.UserMessage("q1"), schema.AssistantMessage("a1", nil)}
	ctx := WithHistory(context.Background(), hist)
	got := runMessages(ctx, "q2")
	if len(got) != 3 || got[0].Content != "q1" || got[1].Content != "a1" || got[2].Content != "q2" || got[2].Role != schema.User {
		t.Fatalf("messages = %+v", got)
	}
	if len(hist) != 2 {
		t.Fatal("history slice was mutated")
	}
	if WithHistory(context.Background(), nil) == nil {
		t.Fatal("nil history must leave ctx usable")
	}
}
