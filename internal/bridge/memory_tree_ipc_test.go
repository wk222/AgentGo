package bridge

import (
	"context"
	"fmt"
	"testing"

	"agentgo/internal/memory"
)

func memoryTreeService(t *testing.T) *AppService {
	t.Helper()
	store, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB().Close() })
	topics := []string{"报表口径", "出差习惯", "公文格式"}
	for i := 0; i < 24; i++ {
		topic := topics[i%len(topics)]
		rec := memory.Record{ID: fmt.Sprintf("m%02d", i), Scope: "sess1", Status: "active", Modality: "fact", Importance: 0.6,
			Content:  fmt.Sprintf("%s 第%d条：口径要写清楚统计周期与单位 report rule %d", topic, i, i),
			Metadata: map[string]any{"topic": topic}}
		if err = store.Ingest(context.Background(), rec); err != nil {
			t.Fatal(err)
		}
	}
	return &AppService{rt: &Runtime{mem: memory.NewPipeline(store)}}
}

func TestMemoryTreeIPCOverviewRefreshAndNodeWalk(t *testing.T) {
	svc := memoryTreeService(t)

	r := svc.GetMemoryTree("sess1", "")
	ov, ok := r["overview"].(memory.TreeOverview)
	if r["success"] != true || !ok || ov.Ready || ov.Reason != "not_built" || ov.Records != 24 {
		t.Fatalf("before refresh: %+v", r)
	}

	r = svc.RefreshMemoryTree("sess1")
	ov, ok = r["overview"].(memory.TreeOverview)
	if r["success"] != true || !ok || !ov.Ready || ov.Covered != 24 || ov.Root == nil || ov.Root.Count != 24 {
		t.Fatalf("after refresh: %+v", r)
	}

	// Expand a branch through the node call, down to a record with its evidence.
	id := ov.Root.Children[0].ID
	for depth := 0; depth < 6; depth++ {
		r = svc.GetMemoryTree("sess1", id)
		node, isNode := r["node"].(memory.TreeNodeView)
		if r["success"] != true || !isNode {
			t.Fatalf("node %s: %+v", id, r)
		}
		if node.RecordID != "" {
			if node.Record == nil || node.Record.Content == "" {
				t.Fatalf("leaf %s has no evidence: %+v", id, node)
			}
			return
		}
		if len(node.Children) == 0 {
			t.Fatalf("branch %s without children: %+v", id, node)
		}
		id = node.Children[0].ID
	}
	t.Fatal("never reached a record")
}

func TestLocateMemoryTreeNodeOpensTheWholePath(t *testing.T) {
	svc := memoryTreeService(t)
	svc.RefreshMemoryTree("sess1")
	ids, _ := svc.PreviewMemoryContext("sess1", "报表口径 report rule 统计周期", 800)["node_ids"].([]string)
	if len(ids) == 0 {
		t.Fatal("no highlighted nodes to locate")
	}
	for _, id := range ids {
		r := svc.LocateMemoryTreeNode("sess1", id)
		chain, ok := r["chain"].([]memory.TreeNodeView)
		if r["success"] != true || !ok || len(chain) == 0 || chain[len(chain)-1].ID != id {
			t.Fatalf("locate %s: %+v", id, r)
		}
		if chain[0].Label != "Memory themes" {
			t.Fatalf("the chain for %s must start at the root: %+v", id, chain[0])
		}
	}
	if r := svc.LocateMemoryTreeNode("sess1", "mt_nope"); r["success"] == true {
		t.Fatalf("an unknown node was located: %+v", r)
	}
}

func TestMemoryTreeIPCRefusesOtherScopesAndEmptyQueries(t *testing.T) {
	svc := memoryTreeService(t)
	ov := svc.RefreshMemoryTree("sess1")["overview"].(memory.TreeOverview)
	if r := svc.GetMemoryTree("someone-else", ov.Root.ID); r["success"] == true {
		t.Fatalf("a node of another scope was served: %+v", r)
	}
	if r := svc.PreviewMemoryContext("sess1", "   ", 0); r["success"] == true {
		t.Fatalf("an empty task must not produce a preview: %+v", r)
	}
	if r := (&AppService{rt: &Runtime{}}).GetMemoryTree("sess1", ""); r["success"] == true {
		t.Fatalf("no memory service must be reported: %+v", r)
	}
}

func TestPreviewMemoryContextMarksTheNodesTheModelSees(t *testing.T) {
	svc := memoryTreeService(t)
	svc.RefreshMemoryTree("sess1")

	r := svc.PreviewMemoryContext("sess1", "报表口径 report rule 统计周期", 800)
	if r["success"] != true || r["hierarchy"] != true {
		t.Fatalf("preview: %+v", r)
	}
	ids, _ := r["node_ids"].([]string)
	if len(ids) == 0 || r["prompt"] == "" {
		t.Fatalf("a ready tree must show tree nodes in the preview: %+v", r)
	}
	if tokens, _ := r["tokens"].(int); tokens <= 0 || tokens > 800 {
		t.Fatalf("preview used %v tokens of 800", r["tokens"])
	}
	for _, id := range ids {
		if n := svc.GetMemoryTree("sess1", id); n["success"] != true {
			t.Fatalf("highlighted node %s cannot be opened: %+v", id, n)
		}
	}
	// The console names each highlighted node without opening it.
	nodes, _ := r["nodes"].([]memory.TreeNodeView)
	if len(nodes) != len(ids) {
		t.Fatalf("%d nodes described for %d ids: %+v", len(nodes), len(ids), r["nodes"])
	}
	for i, n := range nodes {
		if n.ID != ids[i] || n.Label == "" || n.Count == 0 || len(n.Children) != 0 {
			t.Fatalf("node %d is not a bare description of %s: %+v", i, ids[i], n)
		}
	}
}
