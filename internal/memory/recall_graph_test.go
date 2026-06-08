package memory

import (
	"context"
	"testing"
)

func TestBuildRecallGraph(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.DB().Close()

	pipeline := NewPipeline(store)
	engine := &HybridEngine{
		sqlite:   store,
		pipeline: pipeline,
	}

	// Insert some test records
	err = store.Ingest(ctx, Record{
		ID:       "rec1",
		Content:  "Go programming language tips",
		Modality: "journal",
		Scope:    "test_scope",
		Status:   "active",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build the graph
	runnable, err := BuildRecallGraph(ctx)
	if err != nil {
		t.Fatalf("failed to build recall graph: %v", err)
	}

	// Invoke the graph
	results, err := runnable.Invoke(ctx, RecallInput{
		Query: "programming",
		Opts: RecallOptions{
			Scope: "test_scope",
			Limit: 5,
		},
		Config: RecallConfigFromEnv(),
		Engine: engine,
	})
	if err != nil {
		t.Fatalf("failed to invoke recall graph: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected recall results, got none")
	}

	if results[0].ID != "rec1" {
		t.Errorf("expected result ID 'rec1', got %q", results[0].ID)
	}
}

func TestBuildRecallGraphFilters(t *testing.T) {
	ctx := context.Background()
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.DB().Close()

	pipeline := NewPipeline(store)
	engine := &HybridEngine{
		sqlite:   store,
		pipeline: pipeline,
	}

	// Insert some test records with different modality and timestamps
	err = store.Ingest(ctx, Record{
		ID:        "rec1",
		Content:   "Go programming language tips",
		Modality:  "journal",
		Scope:     "test_scope",
		Status:    "active",
		CreatedAt: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	err = store.Ingest(ctx, Record{
		ID:        "rec2",
		Content:   "Go programming logic",
		Modality:  "episode",
		Scope:     "test_scope",
		Status:    "active",
		CreatedAt: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}

	runnable, err := BuildRecallGraph(ctx)
	if err != nil {
		t.Fatalf("failed to build recall graph: %v", err)
	}

	// 1. Filter by Modality
	results, err := runnable.Invoke(ctx, RecallInput{
		Query: "programming",
		Opts: RecallOptions{
			Scope:    "test_scope",
			Limit:    5,
			Modality: "episode",
		},
		Config: RecallConfigFromEnv(),
		Engine: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "rec2" {
		t.Errorf("expected only 'rec2' due to modality filter, got %d results: %+v", len(results), results)
	}

	// 2. Filter by StartTime
	results, err = runnable.Invoke(ctx, RecallInput{
		Query: "programming",
		Opts: RecallOptions{
			Scope:     "test_scope",
			Limit:     5,
			StartTime: 1500,
		},
		Config: RecallConfigFromEnv(),
		Engine: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].ID != "rec2" {
		t.Errorf("expected only 'rec2' due to start time filter, got %d results: %+v", len(results), results)
	}
}
