package memory

import (
	"agentgo/internal/promptbudget"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func testTreeCaller(calls *int) LLMCaller {
	return func(_ context.Context, system, input string) (string, error) {
		if !strings.Contains(system, "covered_children") {
			return "", fmt.Errorf("wrong prompt")
		}
		var children []SummaryNode
		if err := json.Unmarshal([]byte(input), &children); err != nil {
			return "", err
		}
		if len(children) < 2 || len(children) > summaryFanout {
			return "", fmt.Errorf("bad fanout %d", len(children))
		}
		*calls++
		ids := []string{}
		labels := []string{}
		for _, n := range children {
			ids = append(ids, n.ID)
			labels = append(labels, n.Label)
		}
		raw, _ := json.Marshal(map[string]any{"summary": "Themes: " + strings.Join(labels, ", "), "covered_children": ids})
		return string(raw), nil
	}
}
func TestSummaryTreeManyTopicsGrowUpwardAndFitBudget(t *testing.T) {
	s, e := NewSQLiteStore(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB().Close()
	ctx := context.Background()
	const count = 513
	for i := 0; i < count; i++ {
		r := Record{ID: fmt.Sprintf("r%04d", i), Scope: "s", Status: "active", Modality: "reflection", Content: fmt.Sprintf("Previously compressed summary for subject %04d: its decision and unresolved issue remain separately retrievable.", i), Metadata: map[string]any{"topic": fmt.Sprintf("Topic%04d", i)}}
		if e = s.Ingest(ctx, r); e != nil {
			t.Fatal(e)
		}
	}
	f := NewSummaryForest(s)
	calls := 0
	for i := 0; i < 4; i++ {
		if e = f.Refresh(ctx, "s", testTreeCaller(&calls)); e != nil {
			t.Fatal(e)
		}
		root, _, _ := f.root(ctx, "s")
		if root.Complete {
			break
		}
	}
	root, _, e := f.root(ctx, "s")
	if e != nil || root.Count != count || root.Level < 3 || !root.Complete {
		t.Fatal(root, e, calls)
	}
	if calls < 2 {
		t.Fatal("no recursive semantic merging", calls)
	}
	var walk func(SummaryNode, map[string]bool)
	walk = func(n SummaryNode, leaves map[string]bool) {
		if len(n.Children) > summaryFanout {
			t.Fatal("fanout", len(n.Children))
		}
		if n.RecordID != "" {
			if leaves[n.RecordID] {
				t.Fatal("duplicated branch", n.RecordID)
			}
			leaves[n.RecordID] = true
		}
		for _, id := range n.Children {
			c, e := f.node(ctx, "s", id)
			if e != nil {
				t.Fatal(e)
			}
			walk(c, leaves)
		}
	}
	hit, _ := s.GetRecord(ctx, "r0256")
	for _, budget := range []int{0, 40, 80, 200, 400, 1000, 4000} {
		frontier, e := f.Frontier(ctx, "s", []Record{hit}, budget)
		if e != nil {
			t.Fatal(e)
		}
		prompt := renderSummaryFrontier(frontier, budget)
		if promptbudget.Estimate(prompt) > budget {
			t.Fatalf("budget %d used %d", budget, promptbudget.Estimate(prompt))
		}
		if len(frontier) > 0 {
			leaves := map[string]bool{}
			for _, n := range frontier {
				walk(n, leaves)
			}
			if len(leaves) != count {
				t.Fatal("lost coverage", budget, len(leaves))
			}
		}
	}
	before := calls
	if e = f.Refresh(ctx, "s", testTreeCaller(&calls)); e != nil || calls != before {
		t.Fatal("unchanged summaries recompiled", calls, before, e)
	}
	if e = s.Ingest(ctx, Record{ID: "foreign", Scope: "other", Status: "active", Modality: "fact", Content: "SECRET"}); e != nil {
		t.Fatal(e)
	}
	other := NewSummaryForest(s)
	if e = other.Refresh(ctx, "other", nil); e != nil {
		t.Fatal(e)
	}
	foreign, _, _ := other.root(ctx, "other")
	if _, _, e = f.Browse(ctx, "s", foreign.ID, 0, 8); e == nil {
		t.Fatal("cross-scope node read")
	}
	// Every tree leaf can reopen its original record without being overwritten.
	original, e := s.GetRecord(ctx, "r0256")
	if e != nil || original.Content != hit.Content {
		t.Fatal(original, e)
	}
}
func TestSummaryTreeCorrectionRefreshIsLocalAndStaleRootIsRejected(t *testing.T) {
	s, e := NewSQLiteStore(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB().Close()
	ctx := context.Background()
	f := NewSummaryForest(s)
	for i := 0; i < 64; i++ {
		if e = s.Ingest(ctx, Record{ID: fmt.Sprint(i), Scope: "s", Modality: "fact", Status: "active", Content: "source " + strings.Repeat(fmt.Sprint(i), 10), Metadata: map[string]any{"topic": "One topic"}}); e != nil {
			t.Fatal(e)
		}
	}
	calls := 0
	if e = f.Refresh(ctx, "s", testTreeCaller(&calls)); e != nil {
		t.Fatal(e)
	}
	before := calls
	old, _, _ := f.root(ctx, "s")
	rec, _ := s.GetRecord(ctx, "12")
	rec.Content = "USER CORRECTION: earlier success claim is false"
	if e = s.Ingest(ctx, rec); e != nil {
		t.Fatal(e)
	}
	if _, _, e = f.Browse(ctx, "s", old.ID, 0, 8); e == nil {
		t.Fatal("stale source snapshot exposed")
	}
	if e = f.Refresh(ctx, "s", testTreeCaller(&calls)); e != nil {
		t.Fatal(e)
	}
	if delta := calls - before; delta < 1 || delta > 2 {
		t.Fatal("unaffected branches re-summarized", delta)
	}
	// Retraction during model work cannot publish mixed-version evidence.
	rec.Content = "second revision"
	if e = s.Ingest(ctx, rec); e != nil {
		t.Fatal(e)
	}
	changed := false
	fake := testTreeCaller(&calls)
	racing := func(c context.Context, a, b string) (string, error) {
		if !changed {
			changed = true
			if _, e = s.DB().Exec("UPDATE memories SET status='archived' WHERE id='12'"); e != nil {
				t.Fatal(e)
			}
		}
		return fake(c, a, b)
	}
	if e = f.Refresh(ctx, "s", racing); e != ErrSourcesChanged {
		t.Fatal("expected revision rejection", e)
	}
	if _, _, e = f.root(ctx, "s"); e != ErrSourcesChanged {
		t.Fatal("mixed snapshot accepted", e)
	}
}
func TestSummaryTreeInvalidModelOutputKeepsChildren(t *testing.T) {
	s, e := NewSQLiteStore(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB().Close()
	ctx := context.Background()
	f := NewSummaryForest(s)
	for i := 0; i < 10; i++ {
		if e = s.Ingest(ctx, Record{ID: fmt.Sprint(i), Scope: "s", Modality: "fact", Status: "active", Content: "original evidence", Metadata: map[string]any{"topic": fmt.Sprint(i)}}); e != nil {
			t.Fatal(e)
		}
	}
	bad := func(context.Context, string, string) (string, error) {
		return `{"summary":"fabricated","covered_children":["foreign"]}`, nil
	}
	if e = f.Refresh(ctx, "s", bad); e != nil {
		t.Fatal(e)
	}
	root, _, e := f.root(ctx, "s")
	if e != nil || root.Complete || root.Count != 10 {
		t.Fatal(root, e)
	}
	children, _, e := f.Browse(ctx, "s", root.ID, 0, 8)
	if e != nil || len(children) < 2 {
		t.Fatal(children, e)
	}
	if _, _, e = f.Browse(ctx, "s", "mt_unknown", 0, 8); e == nil {
		t.Fatal("accepted unknown node")
	}
}

func TestSummaryWorkerCloseCancelsModelAndRejectsNewWork(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	for i := 0; i < 2; i++ {
		if err = s.Ingest(context.Background(), Record{ID: fmt.Sprint(i), Scope: "s", Modality: "fact", Status: "active", Content: "source evidence"}); err != nil {
			t.Fatal(err)
		}
	}
	f := NewSummaryForest(s)
	defer f.Close()
	entered := make(chan struct{}, 1)
	f.SetCaller(func(ctx context.Context, _, _ string) (string, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return "", ctx.Err()
	})
	f.Request(context.Background(), "s")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never entered model")
	}
	done := make(chan struct{})
	go func() { f.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown failed to cancel memory work")
	}
	f.Request(context.Background(), "s")
	f.mu.Lock()
	running := len(f.running)
	closed := f.closed
	f.mu.Unlock()
	if running != 0 || !closed {
		t.Fatal("worker remained active", running)
	}
}
func TestSummaryCacheCannotDowngradeCompleteNodeAndEmptySnapshotClearsRoutes(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	f := NewSummaryForest(s)
	ctx := context.Background()
	good := SummaryNode{ID: "same", Summary: "complete abstract", Complete: true}
	if err = f.saveNode(ctx, "s", good); err != nil {
		t.Fatal(err)
	}
	if err = f.saveNode(ctx, "s", SummaryNode{ID: "same", Summary: "partial preview"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.node(ctx, "s", "same")
	if err != nil || !got.Complete || got.Summary != good.Summary {
		t.Fatal(got, err)
	}
	if err = s.Ingest(ctx, Record{ID: "r", Scope: "s", Status: "active", Modality: "fact", Content: "evidence"}); err != nil {
		t.Fatal(err)
	}
	if err = f.Refresh(ctx, "s", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB().Exec("UPDATE memories SET status='archived'"); err != nil {
		t.Fatal(err)
	}
	if err = f.Refresh(ctx, "s", nil); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"memory_tree_roots", "memory_tree_members", "memory_tree_routes"} {
		var n int
		if err = s.DB().QueryRow("SELECT COUNT(*) FROM " + table + " WHERE scope='s'").Scan(&n); err != nil || n != 0 {
			t.Fatal(table, n, err)
		}
	}
	var absent *SummaryForest
	if _, _, err = absent.Browse(ctx, "s", "", 0, 8); err == nil {
		t.Fatal("nil forest did not return an error")
	}
}
