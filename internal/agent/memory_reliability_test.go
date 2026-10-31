package agent

import (
	"agentgo/internal/memory"
	"agentgo/internal/sessions"
	"context"
	"errors"
	"github.com/cloudwego/eino/schema"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type recallReliabilityEngine struct {
	memory.Engine
	fn    func(context.Context, string) (string, error)
	calls int
}

func (e *recallReliabilityEngine) ContextPrompt(ctx context.Context, scope string) (string, error) {
	e.calls++
	return e.fn(ctx, scope)
}
func TestExpiredMemoryIsNotResurrectedOnRefreshFailure(t *testing.T) {
	e := &recallReliabilityEngine{fn: func(context.Context, string) (string, error) { return "old fact", nil }}
	m := NewMemoryMiddleware(e)
	ctx := context.Background()
	if m.cachedRecall(ctx, "a") != "old fact" {
		t.Fatal("missing first recall")
	}
	m.cacheMu.Lock()
	m.recallCache["a"] = memoryRecallEntry{prompt: "old fact", expires: time.Now().Add(-time.Second)}
	m.cacheMu.Unlock()
	e.fn = func(context.Context, string) (string, error) { return "", errors.New("store unavailable") }
	if got := m.cachedRecall(ctx, "a"); got != "" {
		t.Fatal("expired facts resurrected", got)
	}
	calls := e.calls
	if got := m.cachedRecall(ctx, "a"); got != "" || e.calls != calls {
		t.Fatal("missing error backoff")
	}
}
func TestRecallFinishingAfterInvalidationCannotRepopulateOldFacts(t *testing.T) {
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan string, 1)
	e := &recallReliabilityEngine{fn: func(context.Context, string) (string, error) { close(started); <-release; return "retracted fact", nil }}
	m := NewMemoryMiddleware(e)
	go func() { done <- m.cachedRecall(context.Background(), "a") }()
	<-started
	m.invalidateRecall("a")
	close(release)
	if got := <-done; got != "" {
		t.Fatal("late stale recall installed", got)
	}
	e.fn = func(context.Context, string) (string, error) { return "corrected fact", nil }
	if got := m.cachedRecall(context.Background(), "a"); got != "corrected fact" {
		t.Fatal(got)
	}
}
func TestDistillationPreservesMultilineMessageBoundaries(t *testing.T) {
	msgs := []*schema.Message{schema.UserMessage("DROP-OLD"), schema.UserMessage("KEEP-FIRST\nconstraint one\nconstraint two"), schema.AssistantMessage("reply one\nmore", nil), schema.UserMessage("last question\nkeep entire body"), schema.AssistantMessage("last reply", nil)}
	out := transcriptFromMessages(msgs, 2)
	if strings.Contains(out, "DROP-OLD") || !strings.Contains(out, "user: KEEP-FIRST\nconstraint one\nconstraint two") || !strings.Contains(out, "user: last question\nkeep entire body") {
		t.Fatal("distillation cut a message by physical lines", out)
	}
	if strings.Count(out, "user:")+strings.Count(out, "assistant:") != 4 {
		t.Fatal(out)
	}
}

func TestRecallCacheChangesWithTaskAndIgnoresHostContext(t *testing.T) {
	e := &recallReliabilityEngine{fn: func(ctx context.Context, _ string) (string, error) { return memory.RecallQueryFromContext(ctx), nil }}
	m := NewMemoryMiddleware(e)
	ctx := context.Background()
	if got := m.cachedRecallForQuery(ctx, "a", "task A"); got != "task A" {
		t.Fatal(got)
	}
	m.cachedRecallForQuery(ctx, "a", "task A")
	if e.calls != 1 {
		t.Fatal("same task lost cache", e.calls)
	}
	if got := m.cachedRecallForQuery(ctx, "a", "task B"); got != "task B" || e.calls != 2 {
		t.Fatal("new task reused old recall", got, e.calls)
	}
	messages := []*schema.Message{schema.UserMessage("REAL-REQUIREMENT"), sessions.ContextMessage("runtime", "HOST-SNAPSHOT"), schema.AssistantMessage("assistant speculation", nil)}
	query := currentMemoryQuery(messages)
	if !strings.Contains(query, "REAL-REQUIREMENT") || strings.Contains(query, "HOST") || strings.Contains(query, "speculation") {
		t.Fatal(query)
	}
}

func TestDistillationBudgetKeepsRecentMessagesAndUnicodeBoundaries(t *testing.T) {
	var msgs []*schema.Message
	for i := 0; i < 30; i++ {
		msgs = append(msgs, schema.UserMessage("HEAD-"+strings.Repeat("\u4e2d\u6587\U0001f408", 5000)+"-TAIL"))
	}
	msgs = append(msgs, schema.UserMessage("LATEST-CORRECTION: preserve tests"))
	out := transcriptFromMessages(msgs, 12)
	if len([]rune(out)) > 16000 {
		t.Fatalf("unbounded journal: %d", len([]rune(out)))
	}
	if !utf8.ValidString(out) || !strings.HasSuffix(out, "user: LATEST-CORRECTION: preserve tests") {
		t.Fatal("lost newest message or damaged Unicode")
	}
	if !strings.Contains(out, "HEAD-") || !strings.Contains(out, "-TAIL") || !strings.Contains(out, "middle omitted") {
		t.Fatal("large messages lost their boundaries or omission marker")
	}
}
