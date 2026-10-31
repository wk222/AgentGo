package memory

import (
	"context"
	"testing"
)

func TestMemoryJournalRepeatedInputDoesNotGrowOrRevive(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p := NewPipeline(s)
	ctx := context.Background()
	calls := 0
	caller := func(context.Context, string, string) (string, error) {
		calls++
		return "User constraint: keep source evidence.", nil
	}
	for i := 0; i < 10; i++ {
		if _, err = p.RunJournal(ctx, caller, "s", "same exact transcript"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListByModality(ctx, "journal", "s", 100)
	if err != nil || len(rows) != 1 || calls != 1 {
		t.Fatal(rows, calls, err)
	}
	if _, err = s.DB().Exec("UPDATE memories SET status='archived' WHERE id=?", rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if text, err := p.RunJournal(ctx, caller, "s", "same exact transcript"); err != nil || text != "" || calls != 1 {
		t.Fatal("revived", text, err, calls)
	}
	if _, err = p.RunJournal(ctx, caller, "other", "same exact transcript"); err != nil || calls != 2 {
		t.Fatal("scope collision", err, calls)
	}
}
func TestMemoryRepeatedDistillationReusesParentAndKeepsRetractions(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p := NewPipeline(s)
	ctx := context.Background()
	if err = p.Ingest(ctx, Record{ID: "j", Scope: "s", Modality: "journal", Content: "keep sources", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	const output = `{"atoms":[{"kind":"constraint","content":"Keep the original source evidence","source_ids":["j"]}]}`
	caller := func(context.Context, string, string) (string, error) { return output, nil }
	for i := 0; i < 4; i++ {
		if _, err = p.RunDistillLLM(ctx, caller, "s", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err = s.DB().QueryRow("SELECT count(*) FROM memories WHERE scope='s'").Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
	id := stableDerivedMemoryID("distill", "s", output)
	if _, err = s.DB().Exec("UPDATE memories SET status='archived' WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if out, err := p.RunDistillLLM(ctx, caller, "s", "", ""); err != nil || out != "" {
		t.Fatal(out, err)
	}
	r, err := s.GetRecord(ctx, id)
	if err != nil || r.Status != "archived" {
		t.Fatal("revived parent", r, err)
	}
}
