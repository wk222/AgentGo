package workflow

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParallelMergeFlow(t *testing.T) {
	mergeID := "merge1"
	doc := FlowgramDocument{
		Nodes: []FlowgramNode{
			{ID: "start", Type: "Start"},
			{ID: "par", Type: "Parallel"},
			{ID: "a", Type: "Code", Data: map[string]any{"code": "A"}},
			{ID: "b", Type: "Code", Data: map[string]any{"code": "B"}},
			{ID: mergeID, Type: "Merge", Data: map[string]any{"merge_strategy": "concat"}},
			{ID: "end", Type: "End"},
		},
		Edges: []FlowgramEdge{
			{SourceNodeID: "start", TargetNodeID: "par"},
			{SourceNodeID: "par", TargetNodeID: "a"},
			{SourceNodeID: "par", TargetNodeID: "b"},
			{SourceNodeID: "a", TargetNodeID: mergeID},
			{SourceNodeID: "b", TargetNodeID: mergeID},
			{SourceNodeID: mergeID, TargetNodeID: "end"},
		},
	}
	def := doc.ToDefinition("t", "")
	var par, merge Node
	for _, n := range def.Nodes {
		if n.ID == "par" {
			par = n
		}
		if n.ID == mergeID {
			merge = n
		}
	}
	if par.Config == nil || par.Config["merge_target"] != mergeID {
		t.Fatalf("parallel merge_target: %#v", par.Config)
	}
	if merge.Config == nil || merge.Config["parallel_id"] != "par" {
		t.Fatalf("merge parallel_id: %#v", merge.Config)
	}

	rc := RunContext{Vars: map[string]string{}}
	parEx := &ParallelNodeExecutor{}
	out, err := parEx.Execute(context.Background(), par, "", "x", rc)
	if err != nil {
		t.Fatal(err)
	}
	mergeEx := &MergeNodeExecutor{}
	merged, err := mergeEx.Execute(context.Background(), merge, "", out, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(merged, "A", "B") {
		t.Fatalf("merge output: %s", merged)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || len(s) > 0 && stringIndex(s, sub) >= 0))
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestApplyMergeStrategyJSONArray(t *testing.T) {
	out := applyMergeStrategy("json_array", []string{"a", "b"})
	var arr []string
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 2 {
		t.Fatalf("got %s", out)
	}
}
