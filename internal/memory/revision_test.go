package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryRevisionSurvivesRestartAndIgnoresRecallCounters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	initial, _ := s.ContextRevision(ctx, "s")
	if err := s.Ingest(ctx, Record{ID: "a", Scope: "s", Content: "compiler A", Modality: "fact", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	version, _ := s.ContextRevision(ctx, "s")
	if version == initial {
		t.Fatal("new fact did not change revision")
	}
	if err := s.IncrementRecallCount(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := s.ContextRevision(ctx, "s")
	if unchanged != version {
		t.Fatal("recall statistics invalidate summaries")
	}
	if err := s.Ingest(ctx, Record{ID: "e", Scope: "s", Content: "ordinary assistant turn", Modality: "episode", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	unchanged, _ = s.ContextRevision(ctx, "s")
	if unchanged != version {
		t.Fatal("ordinary episode invalidates summaries")
	}
	s.DB().Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	restored, _ := s.ContextRevision(ctx, "s")
	if restored != version {
		t.Fatal("revision lost on restart")
	}
	if err := s.Ingest(ctx, Record{ID: "g", Scope: "global", Content: "global preference", Modality: "fact", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	global, _ := s.ContextRevision(ctx, "s")
	if global == version {
		t.Fatal("global correction not reflected in session revision")
	}
}

func TestSupersedeTransactionRollsBackAllFactsAndRevisions(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	ctx := context.Background()
	for _, r := range []Record{{ID: "old", Scope: "s", Content: "old compiler", Modality: "fact", Status: "active"}, {ID: "new", Scope: "s", Content: "new compiler", Modality: "fact", Status: "active"}, {ID: "foreign", Scope: "other", Content: "foreign compiler", Modality: "fact", Status: "active"}} {
		if err := s.Ingest(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := s.ContextRevision(ctx, "s")
	if err := s.SupersedeRecords(ctx, "s", "new", "old", "foreign"); err == nil {
		t.Fatal("cross-scope change allowed")
	}
	old, _ := s.GetRecord(ctx, "old")
	after, _ := s.ContextRevision(ctx, "s")
	if old.Status != "active" || after != before {
		t.Fatal("partial correction was committed", old, after, before)
	}
	if err := s.SupersedeRecords(ctx, "s", "new", "old"); err != nil {
		t.Fatal(err)
	}
	old, _ = s.GetRecord(ctx, "old")
	after, _ = s.ContextRevision(ctx, "s")
	if old.Status != "archived" || old.SupersedesID != "new" || after == before {
		t.Fatal("correction not committed", old)
	}
	if err := s.SupersedeRecords(ctx, "s", "old", "new"); err == nil {
		t.Fatal("obsolete replacement could reactivate a cycle")
	}
}

func TestDistillationRejectsResultsAfterConcurrentCorrection(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p := NewPipeline(s)
	ctx := context.Background()
	if err := p.Ingest(ctx, Record{ID: "j", Scope: "s", Content: "journal source about compiler", Modality: "journal", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	caller := func(context.Context, string, string) (string, error) {
		if err := s.Ingest(ctx, Record{ID: "correction", Scope: "s", Content: "compiler was corrected", Modality: "fact", Status: "active"}); err != nil {
			t.Fatal(err)
		}
		return `{"atoms":[{"kind":"fact","content":"stale compiler choice","source_ids":["j"]}]}`, nil
	}
	if _, err := p.RunDistillLLM(ctx, caller, "s", "", ""); !errors.Is(err, ErrSourcesChanged) {
		t.Fatal("late distillation was published", err)
	}
	rows, err := s.ListByModality(ctx, "fact", "s", 10)
	if err != nil || len(rows) != 1 || rows[0].ID != "correction" {
		t.Fatal(rows, err)
	}
}

func TestFailedMergeDoesNotPublishNewFactOrRevision(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	ctx := context.Background()
	if err := s.Ingest(ctx, Record{ID: "old", Scope: "s", Content: "old source", Modality: "fact", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.ContextRevision(ctx, "s")
	merged := Record{ID: "merge", Scope: "s", Content: "combined source", Modality: "fact", Status: "active"}
	if err := s.mergeAndSupersede(ctx, merged, "old", "missing"); err == nil {
		t.Fatal("merge accepted missing source")
	}
	if _, err := s.GetRecord(ctx, "merge"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("failed merge published replacement", err)
	}
	old, _ := s.GetRecord(ctx, "old")
	after, _ := s.ContextRevision(ctx, "s")
	if old.Status != "active" || before != after {
		t.Fatal("failed merge changed source facts")
	}
}

func TestHybridKeepsLongUnicodeAtomCanonicalID(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	ctx := context.Background()
	h, err := NewHybridEngine(ctx, store, BootConfig{})
	if err != nil {
		t.Fatal(err)
	}
	atom := Record{ID: "atom-long", Scope: "s", Content: strings.Repeat("\u4e2d\u6587", 250), Modality: "fact", Status: "active", Metadata: map[string]interface{}{"source": "llm_distillation", "memory_kind": "constraint", "verification": "derived_unverified"}}
	if err := h.Ingest(ctx, atom); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRecord(ctx, atom.ID)
	if err != nil || got.Content != atom.Content {
		t.Fatal("atomic identifier lost to byte-based chunking", got, err)
	}
	if err := store.MarkSupersedes(ctx, atom.ID, "replacement"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Recall(ctx, "\u4e2d\u6587", RecallOptions{Scope: "s", Limit: 10}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListByModality(ctx, "fact", "s", 20)
	if err != nil || len(rows) != 0 {
		t.Fatal("orphan chunks survived atomic retraction", rows, err)
	}
}

func TestHybridDistillationKeepsParentAndAtomIdentity(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	ctx := context.Background()
	h, err := NewHybridEngine(ctx, store, BootConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Ingest(ctx, Record{ID: "journal-source", Scope: "s", Content: "user constraints about long multilingual project requirements", Modality: "journal", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	atoms := []distilledAtom{}
	for i := 0; i < 3; i++ {
		atoms = append(atoms, distilledAtom{Kind: "constraint", Content: string(rune('A'+i)) + strings.Repeat("\u4e2d\u6587", 230), Sources: []string{"journal-source"}})
	}
	data, err := json.Marshal(map[string]interface{}{"atoms": atoms})
	if err != nil {
		t.Fatal(err)
	}
	caller := func(context.Context, string, string) (string, error) { return string(data), nil }
	if _, err := h.Pipeline().RunDistillLLM(ctx, caller, "s", "", ""); err != nil {
		t.Fatal(err)
	}
	records, err := store.ListByModality(ctx, "fact", "s", 10)
	if err != nil || len(records) != 3 {
		t.Fatal("atoms were split or dropped", len(records), err)
	}
	for _, record := range records {
		parentID, ok := record.Metadata["parent_id"].(string)
		if !ok {
			t.Fatal("no lineage")
		}
		parent, err := store.GetRecord(ctx, parentID)
		if err != nil || parent.Content != string(data) || parent.Metadata["atomic_container"] != true {
			t.Fatal("source parent split into unaddressable chunks", parentID, err)
		}
	}
}
