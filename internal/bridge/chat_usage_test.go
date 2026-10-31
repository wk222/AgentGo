package bridge

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const chatReplyWithUsage = `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens_details":{"reasoning_tokens":5}}}`

const chatReplyWithoutUsage = `{"id":"c2","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`

func chatServer(t *testing.T, body string) LLMConfig {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return LLMConfig{APIBase: srv.URL, APIKey: "test-key", Model: "m"}
}

type bookedCall struct {
	model                                 string
	prompt, cached, completion, reasoning int
	took                                  time.Duration
}

func captureChatUsage(t *testing.T) (calls *[]bookedCall, restore func()) {
	t.Helper()
	var got []bookedCall
	restore = setChatUsageSink(func(m string, prompt, cached, completion, reasoning int, took time.Duration) {
		got = append(got, bookedCall{m, prompt, cached, completion, reasoning, took})
	})
	t.Cleanup(restore)
	return &got, restore
}

// Memory upkeep, workflow nodes and inline edits all go through ChatOnce. Their tokens are billed like the
// chat turns, so they have to show up in the usage figures too.
func TestChatOnceBooksItsTokensInTheUsageLedgerHook(t *testing.T) {
	cfg := chatServer(t, chatReplyWithUsage)
	calls, restore := captureChatUsage(t)

	if _, err := ChatOnce(context.Background(), cfg, "system", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("booked %d calls, want 1", len(*calls))
	}
	got := (*calls)[0]
	if got.model != "m" || got.prompt != 100 || got.cached != 40 || got.completion != 20 || got.reasoning != 5 {
		t.Fatalf("booked %+v, want model m, prompt 100, cached 40, completion 20, reasoning 5", got)
	}
	if got.took <= 0 {
		t.Fatalf("the duration of the call should be booked, got %v", got.took)
	}

	restore()
	if _, err := ChatOnce(context.Background(), cfg, "", "again"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("a removed hook must not be called again, booked %d", len(*calls))
	}
}

// A gateway that returns no usage figures still made a call; the agent loop counts it as zero tokens, so does this.
func TestChatOnceWithoutUsageFiguresStillCountsAsACall(t *testing.T) {
	cfg := chatServer(t, chatReplyWithoutUsage)
	calls, _ := captureChatUsage(t)
	if _, err := ChatOnce(context.Background(), cfg, "", "hello"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].prompt != 0 || (*calls)[0].completion != 0 {
		t.Fatalf("booked %+v, want one call with zero tokens", *calls)
	}
}

func TestAFailedChatOnceBooksNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	calls, _ := captureChatUsage(t)
	if _, err := ChatOnce(context.Background(), LLMConfig{APIBase: srv.URL, APIKey: "k", Model: "m"}, "", "x"); err == nil {
		t.Fatal("expected the gateway error to surface")
	}
	if len(*calls) != 0 {
		t.Fatalf("a call that failed must not be booked: %+v", *calls)
	}
}

// The runtime wires the hook to the daily rollup of its own runner and takes it away when it closes.
func TestRuntimeBooksChatOnceCallsInTheDailyLedger(t *testing.T) {
	rt := boot(t)
	cfg := chatServer(t, chatReplyWithUsage)
	for i := 0; i < 2; i++ {
		if _, err := ChatOnce(context.Background(), cfg, "", "hello"); err != nil {
			t.Fatal(err)
		}
	}
	tracer := rt.agentRunner.UsageTracer()
	if tracer == nil {
		t.Fatal("the runner has no usage tracer")
	}
	sum, err := tracer.GetDailySummary("")
	if err != nil {
		t.Fatal(err)
	}
	calls, prompt, cached := 0, 0, 0
	for _, u := range sum {
		calls += u.Calls
		prompt += u.PromptTokens
		cached += u.CachedTokens
	}
	if calls != 2 || prompt != 200 || cached != 80 {
		t.Fatalf("daily ledger: calls=%d prompt=%d cached=%d, want 2 / 200 / 80", calls, prompt, cached)
	}

	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if chatUsageSink.Load() != nil {
		t.Fatal("closing the runtime must unplug its usage hook")
	}
}
