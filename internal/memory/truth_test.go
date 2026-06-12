package memory

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestMemoryLifecycleStore(t *testing.T) {
	dbFile := "test_memory_lifecycle.db"
	defer os.Remove(dbFile)

	store, err := NewSQLiteStore(dbFile)
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.DB().Close()

	ctx := context.Background()

	// Ingest test record with new fields
	rec := Record{
		ID:           "test_mem_1",
		Content:      "The project name is AgentGo OS.",
		Scope:        "session_1",
		Modality:     "fact",
		Status:       "active",
		Importance:   1.8,
		RecallCount:  3,
		SupersedesID: "old_mem_id",
		IsCanonical:  true,
		SourceTrust:  0.9,
		CreatedAt:    time.Now().Unix(),
		UpdatedAt:    time.Now().Unix(),
	}

	err = store.Ingest(ctx, rec)
	if err != nil {
		t.Fatalf("failed to ingest record: %v", err)
	}

	// Increment recall count
	err = store.IncrementRecallCount(ctx, "test_mem_1")
	if err != nil {
		t.Fatalf("failed to increment recall count: %v", err)
	}

	// Verify updates
	row := store.DB().QueryRow(`SELECT id, content, recall_count, is_canonical, source_trust, supersedes_id FROM memories WHERE id = ?`, "test_mem_1")
	var id, content, supersedesID string
	var recallCount, isCanonical int
	var sourceTrust float64
	err = row.Scan(&id, &content, &recallCount, &isCanonical, &sourceTrust, &supersedesID)
	if err != nil {
		t.Fatalf("failed to scan row: %v", err)
	}

	if recallCount != 4 {
		t.Errorf("expected recall count 4, got %d", recallCount)
	}
	if isCanonical != 1 {
		t.Errorf("expected is_canonical 1, got %d", isCanonical)
	}
	if sourceTrust != 0.9 {
		t.Errorf("expected source trust 0.9, got %f", sourceTrust)
	}
	if supersedesID != "old_mem_id" {
		t.Errorf("expected supersedes_id old_mem_id, got %s", supersedesID)
	}

	// Mark contradiction
	err = store.MarkContradiction(ctx, "test_mem_1", "bad_mem_id")
	if err != nil {
		t.Fatalf("failed to mark contradiction: %v", err)
	}

	var contradictedBy sql.NullString
	_ = store.DB().QueryRow(`SELECT contradicted_by FROM memories WHERE id = ?`, "test_mem_1").Scan(&contradictedBy)
	if !contradictedBy.Valid || contradictedBy.String != "bad_mem_id" {
		t.Errorf("expected contradicted_by bad_mem_id, got %v", contradictedBy)
	}
}

func TestTruthMaintenanceLLM(t *testing.T) {
	ctx := context.Background()

	mockCaller := func(ctx context.Context, system, user string) (string, error) {
		if system == detectSystem {
			return `[{"existing_id": "test_mem_1", "explanation": "Direct contradiction regarding project name"}]`, nil
		}
		if system == resolveSystem {
			return `{"resolution": "supersede_a_with_b", "explanation": "Fact B is newer"}`, nil
		}
		return "", nil
	}

	existing := []Record{
		{ID: "test_mem_1", Content: "The project name is AgentGo."},
	}

	// Test DetectContradictions
	contradictions, err := DetectContradictions(ctx, "The project name is AgentGo OS.", existing, mockCaller)
	if err != nil {
		t.Fatalf("failed to detect contradictions: %v", err)
	}
	if len(contradictions) != 1 || contradictions[0].ExistingID != "test_mem_1" {
		t.Errorf("expected 1 contradiction for test_mem_1, got %+v", contradictions)
	}

	// Test ResolveTruth
	res, err := ResolveTruth(ctx, existing[0], Record{ID: "test_mem_2", Content: "The project name is AgentGo OS."}, mockCaller)
	if err != nil {
		t.Fatalf("failed to resolve truth: %v", err)
	}
	if res.Resolution != "supersede_a_with_b" {
		t.Errorf("expected supersede_a_with_b, got %s", res.Resolution)
	}
}

func TestRecallIncrementsRecallCount(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()

	ctx := context.Background()
	if err := store.Ingest(ctx, Record{
		ID: "recall_1", Content: "AgentGo memory recall counter", Scope: "s1",
		Modality: "fact", Status: "active", CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.Recall(ctx, "AgentGo memory", RecallOptions{Scope: "s1", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("expected recall result")
	}
	got, err := store.GetRecord(ctx, "recall_1")
	if err != nil {
		t.Fatal(err)
	}
	if got.RecallCount != 1 {
		t.Fatalf("expected recall_count 1, got %d", got.RecallCount)
	}
	if got.LastRecallAt == 0 {
		t.Fatal("expected last_recall_at to be set")
	}
}

func TestApplyTruthResolutionSupersedes(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "memory.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()

	ctx := context.Background()
	now := time.Now().Unix()
	factA := Record{ID: "old", Content: "Project name is AgentGo.", Scope: "s1", Modality: "fact", Status: "active", CreatedAt: now, UpdatedAt: now}
	factB := Record{ID: "new", Content: "Project name is AgentGo OS.", Scope: "s1", Modality: "fact", Status: "active", CreatedAt: now, UpdatedAt: now}
	if err := store.Ingest(ctx, factA); err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(ctx, factB); err != nil {
		t.Fatal(err)
	}
	applied, err := ApplyTruthResolution(ctx, store, factA, factB, TruthResolution{Resolution: "supersede_a_with_b"})
	if err != nil {
		t.Fatal(err)
	}
	if applied.ID != "new" || !applied.IsCanonical {
		t.Fatalf("expected canonical new fact, got %+v", applied)
	}
	old, err := store.GetRecord(ctx, "old")
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "archived" || old.SupersedesID != "new" || old.ContradictedBy != "new" {
		t.Fatalf("expected old fact archived and linked to new, got %+v", old)
	}
}
