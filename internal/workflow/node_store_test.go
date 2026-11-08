package workflow

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1) // every :memory: connection is its own database
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNodeStoreVersionsAndRollbackMaterial(t *testing.T) {
	s := newTestStore(t)
	spec := toolSpec()
	m1, err := s.SaveNode(spec)
	if err != nil || m1.Version != 1 {
		t.Fatalf("first save: %+v %v", m1, err)
	}
	spec.Description = "counts words, better"
	m2, err := s.SaveNode(spec)
	if err != nil || m2.Version != 2 || m2.CreatedAt != m1.CreatedAt {
		t.Fatalf("update must bump the version and keep created_at: %+v %v", m2, err)
	}
	got, ok := s.GetNode("WORD_COUNT")
	if !ok || got.Version != 2 || got.Description != "counts words, better" {
		t.Fatalf("GetNode = %+v %v", got, ok)
	}
	old, err := s.GetNodeVersion("word_count", 1)
	if err != nil || old.Description != "counts words" {
		t.Fatalf("version 1 must be kept: %+v %v", old, err)
	}
	vs, _ := s.NodeVersions("word_count")
	if len(vs) != 2 || vs[0] != 1 || vs[1] != 2 {
		t.Fatalf("versions = %v", vs)
	}
	if _, err := s.GetNodeVersion("word_count", 9); err == nil {
		t.Fatal("unknown version must be an error")
	}
}

func TestNodeStoreRejectsInvalidSpec(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SaveNode(NodeSpec{ID: "Bad Id"}); err == nil {
		t.Fatal("an invalid spec must never be stored")
	}
	if _, ok := s.GetNode("bad id"); ok {
		t.Fatal("nothing should have been stored")
	}
}

func TestNodeStoreRetireListAndRevive(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SaveNode(toolSpec()); err != nil {
		t.Fatal(err)
	}
	other := toolSpec()
	other.ID = "another_node"
	if _, err := s.SaveNode(other); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireNode("word_count"); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireNode("ghost_node"); err == nil {
		t.Fatal("retiring an unknown node must fail")
	}
	live, _ := s.ListNodes(false)
	all, _ := s.ListNodes(true)
	if len(live) != 1 || live[0].ID != "another_node" || len(all) != 2 {
		t.Fatalf("live=%d all=%d", len(live), len(all))
	}
	if m, _ := s.GetNode("word_count"); !m.Retired {
		t.Fatal("GetNode still returns a retired node, flagged")
	}
	if m, err := s.SaveNode(toolSpec()); err != nil || m.Retired || m.Version != 2 {
		t.Fatalf("saving revives a retired node: %+v %v", m, err)
	}
}

func TestNodeUsageFindsWorkflowsUsingTheNode(t *testing.T) {
	s := newTestStore(t)
	for id, typ := range map[string]string{"uses_typed": "custom:word_count", "uses_generic": "custom", "unrelated": "llm"} {
		n := Node{ID: "n", Type: typ}
		if typ == "custom" {
			n.Config = map[string]any{"node_id": "word_count"}
		}
		def := Definition{ID: id, Name: id, Nodes: []Node{{ID: "s", Type: "start"}, n, {ID: "e", Type: "end"}}}
		if err := s.Save(def); err != nil {
			t.Fatal(err)
		}
	}
	used := s.NodeUsage("word_count")
	if len(used) != 2 {
		t.Fatalf("usage = %v", used)
	}
}
