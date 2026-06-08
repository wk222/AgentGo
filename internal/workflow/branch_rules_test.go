package workflow

import "testing"

func TestBranchRulesFromTable(t *testing.T) {
	doc := FlowgramDocument{
		Nodes: []FlowgramNode{{
			ID: "if1", Type: "IF",
			Data: map[string]any{
				"branch_table": `[{"id":"a","when":"contains:ok","target":"ok1","isDefault":false},{"id":"b","when":"true","target":"ok2","isDefault":true}]`,
			},
		}},
		Edges: []FlowgramEdge{
			{SourceNodeID: "if1", TargetNodeID: "ok1", When: "contains:ok"},
			{SourceNodeID: "if1", TargetNodeID: "ok2", When: "true"},
		},
	}
	def := doc.ToDefinition("t", "")
	var ifNode Node
	for _, n := range def.Nodes {
		if n.ID == "if1" {
			ifNode = n
			break
		}
	}
	if ifNode.Config == nil {
		t.Fatal("missing config")
	}
	rulesRaw := ifNode.Config["branch_rules"]
	rules, ok := rulesRaw.([]any)
	if !ok || len(rules) != 2 {
		t.Fatalf("branch_rules type %T: %#v", rulesRaw, rulesRaw)
	}
	node := Node{Type: "if", Config: ifNode.Config}
	edges := []Edge{{From: "if1", To: "ok1"}, {From: "if1", To: "ok2"}}
	if got := pickNextEdge(node, "result ok", edges); got != "ok1" {
		t.Fatalf("pick ok branch: got %q", got)
	}
	if got := pickNextEdge(node, "", edges); got != "ok2" {
		t.Fatalf("pick default branch: got %q", got)
	}
}
