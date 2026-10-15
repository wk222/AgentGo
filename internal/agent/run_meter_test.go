package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/ledger"
)

// Every model call must land in the RunMeter (cache + reasoning included) and in the
// ledger rollup, on both the streaming (normal) and blocking path.
func TestRunMeterAndLedgerRecordModelUsage(t *testing.T) {
	srv := newRespServer(t, 200, sse(`{"type":"response.output_text.delta","delta":"hi"}`, completedEv), nil)
	m := testRespModel(srv)

	tracer := ledger.NewModelUsageTracer(t.TempDir(), time.Hour)
	t.Cleanup(func() { _ = tracer.Close() })
	r := &Runner{usageTracer: tracer}

	for _, mode := range []string{"stream", "generate"} {
		meter := &RunMeter{}
		ctx := callbacks.InitCallbacks(context.Background(),
			&callbacks.RunInfo{Name: "t", Type: m.GetType(), Component: components.ComponentOfChatModel},
			r.runUsageHandler(meter))
		in := []*schema.Message{schema.UserMessage("q")}
		if mode == "stream" {
			sr, err := m.Stream(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			for {
				if _, err := sr.Recv(); err != nil {
					break
				}
			}
			sr.Close()
		} else if _, err := m.Generate(ctx, in); err != nil {
			t.Fatal(err)
		}
		meter.Wait(3 * time.Second)
		got := meter.Snapshot()
		want := RunUsage{ModelCalls: 1, PromptTokens: 35, CachedTokens: 4, CompletionTokens: 92, ReasoningTokens: 57}
		if got != want {
			t.Errorf("%s: usage = %+v, want %+v", mode, got, want)
		}
	}

	sum, err := tracer.GetDailySummary("")
	if err != nil {
		t.Fatal(err)
	}
	var calls, cached int
	for _, u := range sum {
		calls += u.Calls
		cached += u.CachedTokens
	}
	if calls != 2 || cached != 8 {
		t.Errorf("ledger calls=%d cached=%d, want 2 and 8 (usage must be recorded for every run)", calls, cached)
	}
}

func TestRunMeterCountsToolCalls(t *testing.T) {
	meter := &RunMeter{}
	ctx := WithRunMeter(context.Background(), meter)
	mw := NewToolCallEventMiddleware().(*ToolCallEventMiddleware)
	ep, err := mw.WrapInvokableToolCall(ctx,
		func(context.Context, string, ...tool.Option) (string, error) { return "ok", nil },
		&adk.ToolContext{Name: "read_file", CallID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := ep(ctx, "{}"); err != nil {
			t.Fatal(err)
		}
	}
	if got := meter.Snapshot().ToolCalls; got != 3 {
		t.Fatalf("ToolCalls = %d, want 3", got)
	}
	// A run without a meter must not panic.
	if _, err := ep(context.Background(), "{}"); err != nil {
		t.Fatal(err)
	}
}

func TestRunUsageCacheHitRate(t *testing.T) {
	if (RunUsage{}).CacheHitRate() != 0 {
		t.Fatal("empty usage must have 0 hit rate")
	}
	if got := (RunUsage{PromptTokens: 200, CachedTokens: 150}).CacheHitRate(); got != 0.75 {
		t.Fatalf("hit rate = %v, want 0.75", got)
	}
}
