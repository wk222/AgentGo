package memory

import (
	"agentgo/internal/promptbudget"
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemoryDirectoryScopePagingAndLiveCorrection(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	ctx := context.Background()
	for _, r := range []Record{
		{ID: "a", Scope: "s", Modality: "fact", Status: "active", Content: "alpha", Metadata: map[string]any{"topic": "Project", "memory_kind": "constraint"}},
		{ID: "b", Scope: "global", Modality: "fact", Status: "active", Content: "beta", Metadata: map[string]any{"topic": "Project", "memory_kind": "constraint"}},
		{ID: "foreign", Scope: "other", Modality: "fact", Status: "active", Content: "SECRET", Metadata: map[string]any{"topic": "Secret"}},
		{ID: "stale", Scope: "s", Modality: "fact", Status: "archived", Content: "STALE", Metadata: map[string]any{"topic": "Old"}},
	} {
		if err = s.Ingest(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	root, err := s.BrowseDirectory(ctx, "s", "", 0, 8)
	if err != nil || len(root.Nodes) != 1 || root.Nodes[0].Count != 2 {
		t.Fatal(root, err)
	}
	kind, err := s.BrowseDirectory(ctx, "s", root.Nodes[0].ID, 0, 8)
	if err != nil || len(kind.Nodes) != 1 {
		t.Fatal(kind, err)
	}
	page, err := s.BrowseDirectory(ctx, "s", kind.Nodes[0].ID, 0, 1)
	if err != nil || len(page.Nodes) != 1 || page.NextOffset != 1 {
		t.Fatal(page, err)
	}
	next, err := s.BrowseDirectory(ctx, "s", kind.Nodes[0].ID, page.NextOffset, 1)
	if err != nil || len(next.Nodes) != 1 || next.Nodes[0].ID == page.Nodes[0].ID || next.NextOffset != 0 {
		t.Fatal(next, err)
	}
	if _, err = s.DB().Exec("UPDATE memories SET status='archived' WHERE id='a'"); err != nil {
		t.Fatal(err)
	}
	root, err = s.BrowseDirectory(ctx, "s", "", 0, 8)
	if err != nil || len(root.Nodes) != 1 || root.Nodes[0].Count != 1 {
		t.Fatal(root, err)
	}
	for _, bad := range []string{"t:", "t:%%%", "t:WA/k:WA/extra", "scope:other"} {
		if _, err = s.BrowseDirectory(ctx, "s", bad, 0, 8); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if _, err = s.BrowseDirectory(ctx, "", "", 0, 8); err == nil {
		t.Fatal("unscoped directory")
	}
}
func TestMemoryTimelineIntervalsRetainOriginals(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	ctx := context.Background()
	for _, item := range []struct{ id, date, scope string }{{"a", "2026-01-01", "s"}, {"b", "2026-12-31", "s"}, {"c", "2025-12-31", "global"}, {"hidden", "2024-01-01", "other"}} {
		date, _ := time.Parse("2006-01-02", item.date)
		if err = s.Ingest(ctx, Record{ID: item.id, Scope: item.scope, Modality: "fact", Status: "active", Content: "original " + item.id, CreatedAt: date.Unix()}); err != nil {
			t.Fatal(err)
		}
	}
	years, err := s.BrowseTimeline(ctx, "s", "", 0, 8)
	if err != nil || len(years.Nodes) != 2 || years.Nodes[0].ID != "time:2026" || years.Nodes[0].Count != 2 {
		t.Fatal(years, err)
	}
	months, err := s.BrowseTimeline(ctx, "s", "time:2026", 0, 8)
	if err != nil || len(months.Nodes) != 2 || months.Nodes[0].ID != "time:2026-12" {
		t.Fatal(months, err)
	}
	leaves, err := s.BrowseTimeline(ctx, "s", "time:2026-01", 0, 8)
	if err != nil || len(leaves.Nodes) != 1 || leaves.Nodes[0].ID != "a" {
		t.Fatal(leaves, err)
	}
	original, err := s.GetRecord(ctx, "a")
	if err != nil || original.Content != "original a" {
		t.Fatal(original, err)
	}
	for _, bad := range []string{"time:2026-13", "time:2026-01-01", "other:2026"} {
		if _, err = s.BrowseTimeline(ctx, "s", bad, 0, 8); err == nil {
			t.Fatal(bad)
		}
	}
}
func TestMemoryDirectoryPromptHonorsMultilingualBudget(t *testing.T) {
	t.Setenv("AGENTGO_MEMORY_HIERARCHY", "1")
	records := []Record{{ID: "a", Status: "active", Scope: "s", Modality: "fact", Content: strings.Repeat("中文证据", 500), Metadata: map[string]any{"topic": "Project", "memory_kind": "constraint"}}}
	for _, n := range []int{0, 30, 200, 1000} {
		result := PromptContext(WithRecallQuery(context.Background(), "Project"), records, n)
		if promptbudget.Estimate(result) > n {
			t.Fatalf("budget=%d got=%d", n, promptbudget.Estimate(result))
		}
	}
	t.Setenv("AGENTGO_MEMORY_TOKENS", "200")
	t.Setenv("AGENTGO_MEMORY_AUTO_TOKENS", "1000")
	if AutomaticTokenBudget() != 200 || PromptTokenBudget(WithPromptBudget(context.Background(), 300)) != 200 {
		t.Fatal("budget clamp")
	}
}
