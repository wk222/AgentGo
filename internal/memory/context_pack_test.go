package memory

import (
	"agentgo/internal/promptbudget"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestContextPackingDeduplicatesAndRejectsStaleEvidence(t *testing.T) {
	rows := []Record{
		{ID: "old", Content: "same fact", Status: "active", UpdatedAt: 1},
		{ID: "new", Content: "same fact", Status: "active", UpdatedAt: 2},
		{ID: "archived", Content: "STALE-A", Status: "archived"},
		{ID: "superseded", Content: "STALE-B", Status: "active", SupersedesID: "new"},
		{ID: "contradicted", Content: "STALE-C", Status: "active", ContradictedBy: "new"},
		{ID: "parent", Content: "DUPLICATE CONTAINER", Status: "active", Metadata: map[string]interface{}{"atomic_children": true}},
	}
	for i := 0; i < 40; i++ {
		rows = append(rows, Record{ID: strings.Repeat("x", i+1), Content: strings.Repeat("\u4e2d\u6587\U0001f408", 300) + string(rune('A'+i)), Status: "active"})
	}
	got := packContextRecords(rows, 1000)
	if promptbudget.Estimate(got) > 1000 || strings.Count(got, "same fact") != 1 || !strings.Contains(got, "id:new") || strings.Contains(got, "STALE-") || strings.Contains(got, "CONTAINER") {
		t.Fatal(got)
	}
}

func TestContextCollectIncludesGlobalAndPreservesCancellation(t *testing.T) {
	calls := 0
	recall := func(ctx context.Context, q string, o RecallOptions) ([]Record, error) {
		calls++
		return []Record{{ID: o.Scope, Scope: o.Scope, Content: q, Status: "active"}}, nil
	}
	rows, err := collectContextRecords(context.Background(), recall, "current-task", "session")
	if err != nil || calls != 2 || len(rows) != 2 || rows[1].Scope != "global" {
		t.Fatal(rows, calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = collectContextRecords(ctx, recall, "task", "session")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAtomicDistillationValidatesSourcesAndRetainsProvenance(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	p := NewPipeline(store)
	ctx := context.Background()
	if err := p.Ingest(ctx, Record{ID: "j1", Scope: "s", Modality: "journal", Status: "active", Content: "User wants tests preserved. Assistant merely claims a build passed."}); err != nil {
		t.Fatal(err)
	}
	caller := func(context.Context, string, string) (string, error) {
		return `{"atoms":[{"kind":"constraint","content":"Preserve existing tests","source_ids":["j1"]},{"kind":"fact","content":"INVENTED VERIFIED RESULT","source_ids":["fake"]},{"kind":"constraint","content":"Preserve existing tests","source_ids":["j1"]}]}`, nil
	}
	for i := 0; i < 2; i++ {
		if _, err := p.RunDistillLLM(ctx, caller, "s", "", "old reference"); err != nil {
			t.Fatal(err)
		}
	}
	records, err := store.ListByModality(ctx, "fact", "s", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Metadata["verification"] != "derived_unverified" || records[0].Metadata["parent_id"] == nil || records[0].SourceTrust >= 1 {
		t.Fatalf("bad atoms: %+v", records)
	}
	all, err := p.List(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	text := packContextRecords(all, 1000)
	if strings.Contains(text, "INVENTED") || strings.Count(text, "Preserve existing tests") != 1 {
		t.Fatal(text)
	}
	rec := records[0]
	rec.Metadata["source_ids"] = []string{"j2"}
	if err := p.Ingest(ctx, rec); err != nil {
		t.Fatal(err)
	}
	updated, err := store.GetRecord(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.TrimSpace(fmtSources(updated.Metadata["source_ids"])), "j2") {
		t.Fatal("provenance upsert was lost", updated.Metadata)
	}
}

func fmtSources(value interface{}) string {
	if rows, ok := value.([]interface{}); ok {
		var out []string
		for _, v := range rows {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return strings.Join(out, ",")
	}
	return ""
}

func TestJournalDoesNotDiscardAnUnresolvedIssueAndPropagatesWriteFailure(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	p := NewPipeline(store)
	caller := func(context.Context, string, string) (string, error) {
		return "\u65e0\u6cd5\u7f16\u8bd1: dependency missing", nil
	}
	if out, err := p.RunJournal(context.Background(), caller, "s", "conversation"); err != nil || out == "" {
		t.Fatal(out, err)
	}
	store.DB().Close()
	if _, err := p.RunJournal(context.Background(), caller, "s", "conversation"); err == nil {
		t.Fatal("hidden persistence failure")
	}
}

func TestDerivedReflectionStaysInProjectScope(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	p := NewPipeline(store)
	ctx := context.Background()
	caller := func(context.Context, string, string) (string, error) {
		return "- Check the project's compiler settings before rebuilding", nil
	}
	if _, err := p.RunReflect(ctx, caller, "project-a", "context"); err != nil {
		t.Fatal(err)
	}
	global, err := store.ListByModality(ctx, "insight", "global", 10)
	if err != nil {
		t.Fatal(err)
	}
	local, err := store.ListByModality(ctx, "insight", "project-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(global) != 0 || len(local) != 1 || local[0].Metadata["verification"] != "derived_unverified" {
		t.Fatal("reflection leaked into global or became verified", global, local)
	}
}

func TestUnsupportedAtomicContainerIsNotRecalled(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	p := NewPipeline(store)
	ctx := context.Background()
	caller := func(context.Context, string, string) (string, error) {
		return `{"atoms":[{"kind":"fact","content":"UNSUPPORTED SUCCESS CLAIM","source_ids":["foreign-source"]}]}`, nil
	}
	if out, err := p.RunDistillLLM(ctx, caller, "s", "unindexed", "context"); err != nil || out != "" {
		t.Fatal(err)
	}
	rows, err := p.List(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := packContextRecords(rows, 1000); got != "" {
		t.Fatal("invalid atoms leaked through parent container", got)
	}
}

func TestStaleVectorCandidateIsCheckedAgainstAuthoritativeStore(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	ctx := context.Background()
	h := &HybridEngine{sqlite: store}
	records := []Record{{ID: "old", Scope: "s", Content: "old value", Modality: "fact", Status: "active"}, {ID: "fresh", Scope: "s", Content: "corrected value", Modality: "fact", Status: "active"}, {ID: "foreign", Scope: "another", Content: "private value", Modality: "fact", Status: "active"}}
	for _, r := range records {
		if err := store.Ingest(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MarkSupersedes(ctx, "old", "fresh"); err != nil {
		t.Fatal(err)
	}
	// Simulate stale status/content/scope returned by the vector index.
	candidates := []Record{{ID: "old", Scope: "s", Status: "active"}, {ID: "fresh", Content: "STALE CONTENT"}, {ID: "foreign", Scope: "s", Status: "active"}, {ID: "deleted", Scope: "s", Status: "active"}}
	got, err := h.currentRecallRecords(ctx, candidates, "s")
	if err != nil || len(got) != 1 || got[0].Content != "corrected value" {
		t.Fatal("stale or foreign candidate leaked", got, err)
	}
}

func TestDistillationDoesNotReactivateArchivedAtom(t *testing.T) {
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB().Close()
	p := NewPipeline(store)
	ctx := context.Background()
	if err := p.Ingest(ctx, Record{ID: "j", Scope: "s", Content: "historical preference about the compiler", Modality: "journal", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	caller := func(context.Context, string, string) (string, error) {
		return `{"atoms":[{"kind":"preference","content":"Use compiler A","source_ids":["j"]}]}`, nil
	}
	if _, err := p.RunDistillLLM(ctx, caller, "s", "", ""); err != nil {
		t.Fatal(err)
	}
	atoms, err := store.ListByModality(ctx, "fact", "s", 10)
	if err != nil || len(atoms) != 1 {
		t.Fatal(atoms, err)
	}
	if err := store.MarkSupersedes(ctx, atoms[0].ID, "correction"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.RunDistillLLM(ctx, caller, "s", "", ""); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetRecord(ctx, atoms[0].ID)
	if err != nil || got.Status != "archived" || got.SupersedesID != "correction" {
		t.Fatal("obsolete atom was revived", got, err)
	}
}
