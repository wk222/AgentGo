package memory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRecallLabelsAssistantClaimsAndKeepsUnicodeValid(t *testing.T) {
	text := formatRecallRecord(Record{ID: "source-1", Content: strings.Repeat("中文🐈", 200), Modality: "episode", Metadata: map[string]interface{}{"verification": "assistant_report_unverified"}}, 301)
	if !utf8.ValidString(text) || !strings.Contains(text, "source-1") || !strings.Contains(text, "未核实") || !strings.Contains(text, "memory excerpt") {
		t.Fatal("invalid/unattributed memory", text)
	}
}
func TestMemoryContextDoesNotHideStoreReadErrors(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHybridEngine(context.Background(), store, BootConfig{})
	if err != nil {
		store.DB().Close()
		t.Fatal(err)
	}
	store.DB().Close()
	for name, engine := range map[string]Engine{"pipeline": NewPipeline(store), "hybrid": h, "enriched": NewEnrichedEngine(h)} {
		if _, err := engine.ContextPrompt(context.Background(), "a"); err == nil {
			t.Fatal(name, "hid a storage failure")
		}
	}
}

func TestReverseModeEpisodesCanBeStoredWithProvenance(t *testing.T) {
	rec := Record{ID: "reverse-episode", Content: "assistant report: analysis attempted", Scope: "conversation", Modality: "episode", Metadata: map[string]interface{}{"mode_profile": "reverse", "verification": "assistant_report_unverified"}}
	if err := NormalizeAndValidateLayer(&rec); err != nil {
		t.Fatal("reverse harness memory rejected", err)
	}
}

func TestAutomaticRecallUsesTaskQueryWithoutLLMRewrite(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "must not rewrite automatic context", 500)
	}))
	defer server.Close()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	h, err := NewHybridEngine(context.Background(), store, BootConfig{})
	if err != nil {
		t.Fatal(err)
	}
	h.apiKey = "test-key"
	h.apiBase = server.URL
	if err := store.Ingest(context.Background(), Record{ID: "task", Scope: "a", Content: "NEEDLE-CODE project regression", Modality: "episode", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	text, err := h.ContextPrompt(WithRecallQuery(context.Background(), "NEEDLE-CODE"), "a")
	if err != nil || !strings.Contains(text, "NEEDLE-CODE") || calls.Load() != 0 {
		t.Fatalf("task recall missed or invoked rewrite: text=%q err=%v requests=%d", text, err, calls.Load())
	}
}
func TestRecallTimeoutKeepsParentDeadlineAndDoesNotRunUnboundedFallback(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	store.DB().SetMaxOpenConns(1)
	held, err := store.DB().Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	h := &HybridEngine{sqlite: store, pipeline: NewPipeline(store)}
	graph, err := BuildRecallGraph(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	cfg := RecallConfigFromEnv()
	cfg.Timeout = 10 * time.Millisecond
	begin := time.Now()
	_, err = graph.Invoke(ctx, RecallInput{Engine: h, Query: "blocked query", Opts: RecallOptions{Scope: "a", Limit: 2}, Config: cfg})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("recall failed to preserve cancellation", err)
	}
	if time.Since(begin) > time.Second {
		t.Fatal("fallback escaped parent deadline")
	}
}
