package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func seedTreeStore(t *testing.T, scope string, perTopic map[string]int) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB().Close() })
	n := 0
	for topic, count := range perTopic {
		for i := 0; i < count; i++ {
			n++
			r := Record{ID: fmt.Sprintf("r%03d", n), Scope: scope, Status: "active", Modality: "fact", Importance: 0.5,
				Content:  fmt.Sprintf("%s fact number %d with enough words to be a real memory", topic, i),
				Metadata: map[string]any{"topic": topic}}
			if err = s.Ingest(context.Background(), r); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s
}

func countView(v TreeNodeView) (records int) {
	if v.RecordID != "" {
		return 1
	}
	for _, c := range v.Children {
		records += c.Count
	}
	return records
}

func TestTreeOverviewStatesAndStructure(t *testing.T) {
	ctx := context.Background()
	s := seedTreeStore(t, "s", map[string]int{"报表口径": 10, "出差习惯": 3, "公文格式": 1})
	f := NewSummaryForest(s)

	// No tree yet: say so, with the number of memories that are waiting.
	ov, err := f.Overview(ctx, "s")
	if err != nil || ov.Ready || ov.Reason != "not_built" || ov.Records != 14 || ov.Root != nil {
		t.Fatalf("before build: %+v err=%v", ov, err)
	}
	empty, err := f.Overview(ctx, "nothing-here")
	if err != nil || empty.Reason != "empty" || empty.Records != 0 {
		t.Fatalf("empty scope: %+v err=%v", empty, err)
	}

	if err = f.RebuildNow(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	ov, err = f.Overview(ctx, "s")
	if err != nil || !ov.Ready || ov.Stale || ov.Covered != 14 || ov.Records != 14 {
		t.Fatalf("after build: %+v err=%v", ov, err)
	}
	if ov.Levels < 3 || ov.Branches == 0 || ov.Nodes != ov.Branches+14 {
		t.Fatalf("levels=%d nodes=%d branches=%d, want >=3 levels and nodes = branches + 14 records", ov.Levels, ov.Nodes, ov.Branches)
	}
	if ov.Summarized != 0 {
		t.Fatalf("no model ran, but %d abstracts are marked as model-written", ov.Summarized)
	}
	if ov.Root == nil || ov.Root.Count != 14 || len(ov.Root.Children) == 0 || len(ov.Root.Children) > summaryFanout {
		t.Fatalf("root view: %+v", ov.Root)
	}
	sum := 0
	for _, c := range ov.Root.Children {
		sum += c.Count
	}
	if sum != 14 {
		t.Fatalf("children of the root cover %d records, want 14", sum)
	}

	// Walking down through Subtree reaches every record exactly once, with its evidence.
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		v, e := f.Subtree(ctx, "s", id)
		if e != nil {
			t.Fatal(e)
		}
		if v.RecordID != "" {
			if v.Record == nil || !strings.Contains(v.Record.Content, "fact number") || v.Record.Modality != "fact" {
				t.Fatalf("leaf without evidence: %+v", v)
			}
			if seen[v.RecordID] {
				t.Fatalf("record %s reached twice", v.RecordID)
			}
			seen[v.RecordID] = true
			return
		}
		if len(v.Children) != v.ChildCount || countView(v) != v.Count {
			t.Fatalf("node %s shows %d/%d children, %d/%d records", v.ID, len(v.Children), v.ChildCount, countView(v), v.Count)
		}
		for _, c := range v.Children {
			walk(c.ID)
		}
	}
	walk(ov.Root.ID)
	if len(seen) != 14 {
		t.Fatalf("walk reached %d records, want 14", len(seen))
	}
	if _, err = f.Subtree(ctx, "s", "mt_doesnotexist"); err == nil {
		t.Fatal("an id outside the snapshot must be refused")
	}
	if _, err = f.Subtree(ctx, "elsewhere", ov.Root.ID); err == nil {
		t.Fatal("a node of another scope must be refused")
	}

	// Model-written abstracts are counted separately from structural ones.
	calls := 0
	if err = f.Refresh(ctx, "s", testTreeCaller(&calls)); err != nil {
		t.Fatal(err)
	}
	ov, _ = f.Overview(ctx, "s")
	if ov.Summarized == 0 || ov.Summarized != ov.Branches {
		t.Fatalf("summarized=%d branches=%d after a complete semantic pass", ov.Summarized, ov.Branches)
	}

	// A new memory makes the snapshot stale but it can still be drawn.
	if err = s.Ingest(ctx, Record{ID: "late", Scope: "s", Status: "active", Modality: "fact", Content: "A late memory", Metadata: map[string]any{"topic": "出差习惯"}}); err != nil {
		t.Fatal(err)
	}
	ov, err = f.Overview(ctx, "s")
	if err != nil || ov.Ready || !ov.Stale || ov.Reason != "stale" || ov.Root == nil || ov.Covered != 14 || ov.Records != 15 {
		t.Fatalf("stale: %+v err=%v", ov, err)
	}
	if err = f.RebuildNow(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	if ov, _ = f.Overview(ctx, "s"); !ov.Ready || ov.Covered != 15 {
		t.Fatalf("after rebuild: %+v", ov)
	}
}

func TestStructuralAbstractIsReadableAndLeavesKeepTheirEvidence(t *testing.T) {
	ctx := context.Background()
	s := seedTreeStore(t, "s", map[string]int{"报表口径": 12, "出差习惯": 4})
	f := NewSummaryForest(s)
	if err := f.RebuildNow(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	ov, _ := f.Overview(ctx, "s")

	branches, leaves := 0, 0
	var walk func(id string)
	walk = func(id string) {
		v, err := f.Subtree(ctx, "s", id)
		if err != nil {
			t.Fatal(err)
		}
		if v.RecordID != "" {
			leaves++
			// The model must still be able to cite a record and to see how far to trust it.
			if !strings.Contains(v.Summary, "[id:") || !strings.Contains(v.Summary, "历史参考") {
				t.Fatalf("leaf %s lost its id or evidence label: %q", v.ID, v.Summary)
			}
			return
		}
		branches++
		if strings.Contains(v.Summary, "[id:") || strings.Contains(v.Summary, "历史参考") {
			t.Fatalf("branch %s spends its tokens on record bookkeeping: %q", v.ID, v.Summary)
		}
		if strings.Count(v.Summary, v.Label+":") > 0 && v.Label != "Memory themes" {
			t.Fatalf("branch %s repeats its own topic label: %q", v.ID, v.Summary)
		}
		if !strings.Contains(v.Summary, "fact number") {
			t.Fatalf("branch %s carries none of its children's text: %q", v.ID, v.Summary)
		}
		for _, c := range v.Children {
			walk(c.ID)
		}
	}
	walk(ov.Root.ID)
	if leaves != 16 || branches != ov.Branches {
		t.Fatalf("walked %d leaves and %d branches; overview says 16 records and %d branches", leaves, branches, ov.Branches)
	}
	// At the top the topics are told apart by their labels.
	if !strings.Contains(ov.Root.Summary, "报表口径:") && !strings.Contains(ov.Root.Summary, "出差习惯:") {
		t.Fatalf("the root does not name its topics: %q", ov.Root.Summary)
	}
}

func TestLocateReturnsTheChainFromTheRootDown(t *testing.T) {
	ctx := context.Background()
	s := seedTreeStore(t, "s", map[string]int{"甲": 30, "乙": 5})
	f := NewSummaryForest(s)
	if err := f.RebuildNow(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	ov, _ := f.Overview(ctx, "s")

	// Every node of the tree, found by walking it, is located by a chain that starts at the root,
	// ends at the node, and in which each element lists the next one among its children.
	checked := 0
	var walk func(id string)
	walk = func(id string) {
		chain, err := f.Locate(ctx, "s", id)
		if err != nil {
			t.Fatalf("locate %s: %v", id, err)
		}
		if chain[0].ID != ov.Root.ID || chain[len(chain)-1].ID != id {
			t.Fatalf("chain for %s runs %s … %s", id, chain[0].ID, chain[len(chain)-1].ID)
		}
		for i := 0; i+1 < len(chain); i++ {
			found := false
			for _, c := range chain[i].Children {
				found = found || c.ID == chain[i+1].ID
			}
			if !found || chain[i].Level <= chain[i+1].Level {
				t.Fatalf("chain for %s breaks between %s and %s", id, chain[i].ID, chain[i+1].ID)
			}
		}
		checked++
		v, _ := f.Subtree(ctx, "s", id)
		for _, c := range v.Children {
			walk(c.ID)
		}
	}
	walk(ov.Root.ID)
	if checked != ov.Nodes {
		t.Fatalf("located %d nodes of %d", checked, ov.Nodes)
	}
	if _, err := f.Locate(ctx, "s", "mt_nope"); err == nil {
		t.Fatal("an unknown node must not be located")
	}
	if _, err := f.Locate(ctx, "elsewhere", ov.Root.ID); err == nil {
		t.Fatal("a node of another scope must not be located")
	}
}

func TestFrontierNodeIDsMatchWhatTheModelReads(t *testing.T) {
	ctx := context.Background()
	s := seedTreeStore(t, "s", map[string]int{"甲": 9, "乙": 9, "丙": 9})
	f := NewSummaryForest(s)
	if err := f.RebuildNow(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	hit, _ := s.GetRecord(ctx, "r001")
	frontier, err := f.Frontier(ctx, "s", []Record{hit}, 400)
	if err != nil || len(frontier) == 0 {
		t.Fatalf("frontier: %v %v", frontier, err)
	}
	prompt := renderSummaryFrontier(frontier, 400)
	ids := FrontierNodeIDs(prompt)
	if len(ids) != len(frontier) {
		t.Fatalf("parsed %d ids from a prompt that lists %d nodes:\n%s", len(ids), len(frontier), prompt)
	}
	for i, n := range frontier {
		if ids[i] != n.ID {
			t.Fatalf("id %d = %s, want %s", i, ids[i], n.ID)
		}
	}
	if got := FrontierNodeIDs("- fact: nothing to see here\n[node=not-a-node; n=2]"); len(got) != 0 {
		t.Fatalf("flat recall text has no tree nodes, got %v", got)
	}
}
