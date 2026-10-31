package memory

import (
	"context"
	"errors"
	"github.com/cloudwego/eino/schema"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedEpisodicSummaryDoesNotBlockOrDuplicate(t *testing.T) {
	t.Setenv("AGENTGO_EPISODIC_COMPRESS", "1")
	started := make(chan struct{}, 2)
	done := make(chan struct{}, 2)
	var calls atomic.Int32
	c := NewEpisodicCompressor(NewPipeline(nil), func(ctx context.Context, _, _ string) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		done <- struct{}{}
		return "", errors.New("cancelled")
	})
	c.summaries["s"] = "previous successful summary"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan string, 1)
	msgs := []*schema.Message{schema.UserMessage("request")}
	go func() { returned <- c.CachedSummary(ctx, "s", msgs, true) }()
	select {
	case got := <-returned:
		if got != "previous successful summary" {
			t.Fatal("lost cached summary")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked on memory LLM")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	for i := 0; i < 5; i++ {
		if c.CachedSummary(ctx, "s", msgs, true) != "previous successful summary" {
			t.Fatal("cached summary vanished")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate distillation workers")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored cancellation")
	}
}
func TestCompressionTranscriptBounded(t *testing.T) {
	s := transcriptForCompress([]*schema.Message{schema.UserMessage(strings.Repeat("中文", 50000))}, 24)
	if len([]rune(s)) > 4100 {
		t.Fatal("unbounded transcript")
	}
}
