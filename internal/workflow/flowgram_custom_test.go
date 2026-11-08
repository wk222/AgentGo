package workflow

import (
	"encoding/json"
	"testing"
)

// canvasTrip is what the editor does to a workflow: definition -> canvas JSON -> (browser) -> definition.
func canvasTrip(t *testing.T, def Definition) (FlowgramDocument, Definition) {
	t.Helper()
	doc := FromDefinition(def)
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var back FlowgramDocument
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	return doc, back.ToDefinition(def.Name, def.Description)
}

func TestCanvasKeepsCustomNodes(t *testing.T) {
	// ids that contain "end", "code", "tool" and "agent" used to be mistaken for built-in types
	for _, id := range []string{"send_email_node", "code_formatter", "tool_picker", "agent_router", "plain_node"} {
		def := Definition{
			ID: "wf", Name: "wf",
			Nodes: []Node{
				{ID: "s", Type: "start"},
				{ID: "c", Type: "custom:" + id, Config: map[string]any{"inputs": map[string]any{"to": "{{input}}", "n": 3.0}}},
				{ID: "e", Type: "end"},
			},
			Edges: []Edge{{From: "s", To: "c"}, {From: "c", To: "e"}},
		}
		doc, back := canvasTrip(t, def)

		var node FlowgramNode
		for _, n := range doc.Nodes {
			if n.ID == "c" {
				node = n
			}
		}
		if node.Type != "Custom" || node.Data["node_id"] != id {
			t.Fatalf("%s: canvas node = %+v", id, node)
		}
		text, isText := node.Data["inputs"].(string)
		if !isText {
			t.Fatalf("%s: the canvas holds inputs as JSON text (it turns values into strings): %#v", id, node.Data["inputs"])
		}
		var shown map[string]any
		if err := json.Unmarshal([]byte(text), &shown); err != nil || shown["to"] != "{{input}}" {
			t.Fatalf("%s: inputs text = %q (%v)", id, text, err)
		}

		var got Node
		for _, n := range back.Nodes {
			if n.ID == "c" {
				got = n
			}
		}
		if got.Type != "custom:"+id {
			t.Fatalf("%s: after a round trip the node is %q", id, got.Type)
		}
		in, _ := got.Config["inputs"].(map[string]any)
		if in["to"] != "{{input}}" || in["n"] != 3.0 {
			t.Fatalf("%s: inputs after the round trip = %#v", id, got.Config["inputs"])
		}
	}
}

func TestCanvasTypesForCustomNodes(t *testing.T) {
	for _, in := range []string{"custom", "Custom", "custom_node", "custom:send_email", "CUSTOM:x_node"} {
		if got := normalizeNodeType(in); got != "custom" {
			t.Errorf("normalizeNodeType(%q) = %q", in, got)
		}
	}
	if flowgramType("custom") != "Custom" {
		t.Fatal("canvas type")
	}
}

func TestCanvasNodeAddedByHandNeedsNoSpecialCasing(t *testing.T) {
	doc := FlowgramDocument{Nodes: []FlowgramNode{
		{ID: "s", Type: "Start"},
		{ID: "c", Type: "Custom", Data: map[string]any{"node_id": " Geo_Lookup ", "inputs": "{\"city\": \"{{input}}\",\n \"limit\": 3}"}},
		{ID: "e", Type: "End"},
	}}
	def := doc.ToDefinition("n", "")
	var got Node
	for _, n := range def.Nodes {
		if n.ID == "c" {
			got = n
		}
	}
	in, _ := got.Config["inputs"].(map[string]any)
	if got.Type != "custom:geo_lookup" || in["city"] != "{{input}}" || in["limit"] != 3.0 {
		t.Fatalf("node = %+v", got)
	}
}

func TestCanvasKeepsBrokenInputsTextAndEmptyNodeID(t *testing.T) {
	doc := FlowgramDocument{Nodes: []FlowgramNode{
		{ID: "c", Type: "Custom", Data: map[string]any{"node_id": "", "inputs": "{not json"}},
		{ID: "d", Type: "Custom", Data: map[string]any{"node_id": "ok_node", "inputs": "  "}},
	}}
	def := doc.ToDefinition("n", "")
	if def.Nodes[0].Type != "custom" || def.Nodes[0].Config["inputs"] != "{not json" {
		t.Fatalf("what the user typed must survive until they fix it: %+v", def.Nodes[0])
	}
	if _, has := def.Nodes[1].Config["inputs"]; has || def.Nodes[1].Type != "custom:ok_node" {
		t.Fatalf("blank inputs text is no inputs: %+v", def.Nodes[1])
	}
	// and the broken node round-trips back onto the canvas unchanged
	back := FromDefinition(def)
	if back.Nodes[0].Type != "Custom" || back.Nodes[0].Data["inputs"] != "{not json" {
		t.Fatalf("canvas node = %+v", back.Nodes[0])
	}
}
