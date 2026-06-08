package workflow

import "testing"

func TestFlowgramEdgeWhenRoundTrip(t *testing.T) {
	doc := FlowgramDocument{
		Nodes: []FlowgramNode{
			{ID: "start", Type: "Start"},
			{ID: "br", Type: "Branch", Data: map[string]any{"branches": `["true","false"]`}},
			{ID: "ok", Type: "End"},
			{ID: "no", Type: "End"},
		},
		Edges: []FlowgramEdge{
			{SourceNodeID: "start", TargetNodeID: "br"},
			{SourceNodeID: "br", TargetNodeID: "ok", SourcePortID: "true", When: "true", Label: "true"},
			{SourceNodeID: "br", TargetNodeID: "no", SourcePortID: "false", When: "false", Label: "false"},
		},
	}
	def := doc.ToDefinition("t", "")
	if len(def.Edges) != 3 {
		t.Fatalf("edges: got %d", len(def.Edges))
	}
	var gotTrue, gotFalse bool
	for _, e := range def.Edges {
		if e.From == "br" && e.To == "ok" && e.When == "true" {
			gotTrue = true
		}
		if e.From == "br" && e.To == "no" && e.When == "false" {
			gotFalse = true
		}
	}
	if !gotTrue || !gotFalse {
		t.Fatalf("branch when labels missing: true=%v false=%v", gotTrue, gotFalse)
	}
}
