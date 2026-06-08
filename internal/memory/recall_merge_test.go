package memory

import (
	"context"
	"testing"
)

func TestFtsMatchQuery(t *testing.T) {
	q := ftsMatchQuery("session context user goals")
	if q == "" || q == `""` {
		t.Fatalf("query=%q", q)
	}
	if q != `"session" OR "context" OR "user" OR "goals"` {
		t.Fatalf("got %q", q)
	}
}

func TestMergeHybridRRF(t *testing.T) {
	a := []Record{{ID: "1", Content: "a"}, {ID: "2", Content: "b"}}
	b := []Record{{ID: "2", Content: "b"}, {ID: "3", Content: "c"}}
	out := mergeHybridRRF([][]Record{a, b}, []float64{0.5, 0.5}, 3)
	if len(out) != 3 {
		t.Fatalf("len=%d", len(out))
	}
	if out[0].ID != "2" {
		t.Fatalf("expected id 2 on top, got %+v", out)
	}
}

func TestRecallLikeFallback(t *testing.T) {
	db, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	_ = db.Ingest(ctx, Record{
		ID: "r1", Content: "user prefers dark mode", Scope: "sess-1",
		Modality: "fact", Status: "active", Importance: 1,
		CreatedAt: 1, UpdatedAt: 1,
	})
	rows, err := db.RecallLikeFallback(ctx, "dark mode", RecallOptions{Scope: "sess-1", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "r1" {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestRecallGraphExpansionRespectsLimit(t *testing.T) {
	db, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.DB().Close()

	ctx := t.Context()
	records := []Record{
		{ID: "r1", Content: "alpha root memory", Scope: "sess-1", Modality: "fact", Status: "active", Importance: 1, CreatedAt: 1, UpdatedAt: 1},
		{ID: "r2", Content: "linked neighbor one", Scope: "sess-1", Modality: "fact", Status: "active", Importance: 1, CreatedAt: 1, UpdatedAt: 1},
		{ID: "r3", Content: "linked neighbor two", Scope: "sess-1", Modality: "fact", Status: "active", Importance: 1, CreatedAt: 1, UpdatedAt: 1},
	}
	for _, r := range records {
		if err := db.Ingest(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Link(ctx, "r1", "r2", "related"); err != nil {
		t.Fatal(err)
	}
	if err := db.Link(ctx, "r1", "r3", "related"); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Recall(ctx, "alpha", RecallOptions{Scope: "sess-1", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("recall returned %d rows, want limit 1: %+v", len(rows), rows)
	}
}

func TestMergeHybridRRFEdgeCases(t *testing.T) {
	only := []Record{{ID: "1"}, {ID: "2"}}

	// Single populated stream alongside an empty one: order is preserved, no panic.
	out := mergeHybridRRF([][]Record{only, {}}, []float64{0.5, 0.5}, 5)
	if len(out) != 2 || out[0].ID != "1" || out[1].ID != "2" {
		t.Fatalf("single-stream fusion wrong: %+v", out)
	}

	// All-empty / nil inputs must yield an empty (non-nil) slice without panicking.
	if got := mergeHybridRRF([][]Record{{}, nil}, nil, 5); len(got) != 0 {
		t.Fatalf("empty fusion got %+v", got)
	}
	if got := mergeHybridRRF(nil, nil, 0); len(got) != 0 {
		t.Fatalf("nil fusion got %+v", got)
	}

	// Records with empty IDs are dropped (cannot be ranked/deduped).
	if got := mergeHybridRRF([][]Record{{{ID: ""}, {ID: "x"}}}, nil, 5); len(got) != 1 || got[0].ID != "x" {
		t.Fatalf("empty-id handling got %+v", got)
	}
}

// TestRecallGraphDegradesToLikeFallback drives the full Eino recall graph with a query
// that FTS tokenization cannot match but a substring LIKE scan can, proving the
// degradation path recovers results instead of returning nothing.
func TestRecallGraphDegradesToLikeFallback(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()

	ctx := context.Background()
	if err := store.Ingest(ctx, Record{
		ID: "r1", Content: "supercalifragilistic note", Scope: "s1",
		Modality: "fact", Status: "active", Importance: 1, CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}

	engine := &HybridEngine{sqlite: store, pipeline: NewPipeline(store)}

	runnable, err := BuildRecallGraph(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// "califragi" is a substring of the content but not a tokenized FTS term.
	got, err := runnable.Invoke(ctx, RecallInput{
		Query:  "califragi",
		Opts:   RecallOptions{Scope: "s1", Limit: 5},
		Config: RecallConfigFromEnv(),
		Engine: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("degrade fallback did not recover record: %+v", got)
	}
}
