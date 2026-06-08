package workflow

import (
	"context"
	"encoding/json"
	"testing"
)

func TestLoopNodeExecutorSubFlowgram(t *testing.T) {
	sub := FlowgramDocument{
		Nodes: []FlowgramNode{
			{ID: "s", Type: "Start"},
			{ID: "c", Type: "Code", Data: map[string]any{"code": "{{var.item}}"}},
			{ID: "e", Type: "End"},
		},
		Edges: []FlowgramEdge{
			{SourceNodeID: "s", TargetNodeID: "c"},
			{SourceNodeID: "c", TargetNodeID: "e"},
		},
	}
	subB, _ := json.Marshal(sub)
	node := Node{
		ID: "loop1", Type: "loop",
		Config: map[string]any{
			"collection":    `["a","b"]`,
			"item_var":      "item",
			"sub_flowgram":  string(subB),
			"max_iterations": 10,
		},
	}
	ex := &LoopNodeExecutor{}
	out, err := ex.Execute(context.Background(), node, "", `[]`, RunContext{Vars: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	var arr []string
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("output not json array: %s", out)
	}
	if len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
		t.Fatalf("unexpected loop output: %v", arr)
	}
}
