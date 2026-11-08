package workflow

import (
	"encoding/json"
	"strings"
)

// The canvas knows a custom node as type "Custom" with two fields: node_id and
// inputs (JSON text). The definition keeps type "custom:<id>" and config.inputs.
// Without this the editor would turn every custom node into an LLM node the next
// time somebody opens and saves a workflow an agent built.

// ingestCustomNode turns the canvas fields of a custom node back into definition form.
func ingestCustomNode(node *Node, data map[string]any) {
	if s, ok := data["node_id"].(string); ok {
		if id := strings.ToLower(strings.TrimSpace(s)); id != "" {
			node.Type = CustomNodePrefix + id
		}
	}
	switch v := data["inputs"].(type) {
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return
		}
		var obj map[string]any
		if json.Unmarshal([]byte(text), &obj) == nil && obj != nil {
			node.Config["inputs"] = obj
		} else {
			node.Config["inputs"] = v // keep what the user typed; the run and Lint both report it
		}
	case map[string]any:
		node.Config["inputs"] = v
	}
}

// exportCustomNode fills the canvas node for a custom workflow node.
func exportCustomNode(fn *FlowgramNode, n Node) {
	fn.Type = "Custom"
	fn.Data["node_id"] = CustomNodeID(n)
	switch v := n.Config["inputs"].(type) {
	case nil:
	case string:
		fn.Data["inputs"] = v
	default:
		if b, err := json.Marshal(v); err == nil {
			fn.Data["inputs"] = string(b)
		}
	}
}
