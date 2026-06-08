package workflow

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestHappyPathDefinitionChain(t *testing.T) {
	def := HappyPathDefinition()
	if len(def.Nodes) != 5 || len(def.Edges) != 4 {
		t.Fatalf("nodes/edges: %d %d", len(def.Nodes), len(def.Edges))
	}
	doc := HappyPathFlowgram()
	back := doc.ToDefinition("x", "y")
	var toolName, notifyType string
	for _, n := range back.Nodes {
		if n.ID == "tool1" {
			toolName = n.ToolName
		}
		if n.ID == "notify1" {
			notifyType = n.Type
		}
	}
	if toolName != "get_current_time" {
		t.Fatalf("tool: %q", toolName)
	}
	if notifyType != "notify" {
		t.Fatalf("notify: %q", notifyType)
	}
}

func TestEnsureHappyPathTemplate(t *testing.T) {
	db, _ := sql.Open("sqlite", ":memory:")
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureHappyPathTemplate(); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(HappyPathWorkflowID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name == "" {
		t.Fatal("empty name")
	}
	_ = context.Background()
}
