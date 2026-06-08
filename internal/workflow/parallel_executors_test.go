package workflow

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParallelNodeExecutor(t *testing.T) {
	subA := FlowgramDocument{
		Nodes: []FlowgramNode{
			{ID: "s", Type: "Start"},
			{ID: "c", Type: "Code", Data: map[string]any{"code": "A"}},
			{ID: "e", Type: "End"},
		},
		Edges: []FlowgramEdge{
			{SourceNodeID: "s", TargetNodeID: "c"},
			{SourceNodeID: "c", TargetNodeID: "e"},
		},
	}
	subB := FlowgramDocument{
		Nodes: []FlowgramNode{
			{ID: "s", Type: "Start"},
			{ID: "c", Type: "Code", Data: map[string]any{"code": "B"}},
			{ID: "e", Type: "End"},
		},
		Edges: subA.Edges,
	}
	ba, _ := json.Marshal(subA)
	bb, _ := json.Marshal(subB)
	node := Node{
		ID: "par1", Type: "parallel",
		Config: map[string]any{
			"merge_strategy": "json_array",
			"parallel_branches": []any{
				map[string]any{"target": "a", "flowgram": string(ba)},
				map[string]any{"target": "b", "flowgram": string(bb)},
			},
		},
	}
	ex := &ParallelNodeExecutor{}
	out, err := ex.Execute(context.Background(), node, "", "", RunContext{Vars: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	var arr []string
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("output: %s", out)
	}
	if len(arr) != 2 || arr[0] != "A" || arr[1] != "B" {
		t.Fatalf("unexpected parallel output: %v", arr)
	}
}
