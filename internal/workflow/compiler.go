package workflow

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/compose"
)

// VueFlowDocument represents the VueFlow frontend layout schema.
type VueFlowDocument struct {
	Nodes []VueFlowNode `json:"nodes"`
	Edges []VueFlowEdge `json:"edges"`
}

// VueFlowNode is a single node in the VueFlow layout.
type VueFlowNode struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Position VueFlowPos     `json:"position"`
	Data     map[string]any `json:"data,omitempty"`
}

// VueFlowPos is the 2D layout coordinate.
type VueFlowPos struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// VueFlowEdge is a single connecting edge in the VueFlow layout.
type VueFlowEdge struct {
	ID     string         `json:"id"`
	Source string         `json:"source"`
	Target string         `json:"target"`
	Label  string         `json:"label,omitempty"`
	Data   map[string]any `json:"data,omitempty"`
}

// ToFlowgram translates VueFlow canvas structure to PyFlow/Flowgram layout format.
// Replaces reserved keywords "start" and "end" to prevent Eino Graph build collisions.
func (vdoc VueFlowDocument) ToFlowgram() FlowgramDocument {
	fdoc := FlowgramDocument{
		Nodes: make([]FlowgramNode, 0, len(vdoc.Nodes)),
		Edges: make([]FlowgramEdge, 0, len(vdoc.Edges)),
	}

	for _, vn := range vdoc.Nodes {
		id := vn.ID
		if id == "start" {
			id = "start_node"
		} else if id == "end" {
			id = "end_node"
		}
		fn := FlowgramNode{
			ID:   id,
			Type: vn.Type,
			Data: vn.Data,
		}
		fn.Meta.Position.X = vn.Position.X
		fn.Meta.Position.Y = vn.Position.Y
		fdoc.Nodes = append(fdoc.Nodes, fn)
	}

	for _, ve := range vdoc.Edges {
		src := ve.Source
		if src == "start" {
			src = "start_node"
		} else if src == "end" {
			src = "end_node"
		}
		tgt := ve.Target
		if tgt == "start" {
			tgt = "start_node"
		} else if tgt == "end" {
			tgt = "end_node"
		}
		fe := FlowgramEdge{
			SourceNodeID: src,
			TargetNodeID: tgt,
			Label:        ve.Label,
		}
		if fe.Label == "" && ve.Data != nil {
			if w, ok := ve.Data["when"].(string); ok {
				fe.Label = w
			}
		}
		fdoc.Edges = append(fdoc.Edges, fe)
	}

	return fdoc
}

// CompileVueFlow translates a VueFlow JSON topology into a runnable Eino Compose Runnable Graph.
func CompileVueFlow(ctx context.Context, jsonStr string, rc *RunContext) (compose.Runnable[*WorkflowState, *WorkflowState], error) {
	var vdoc VueFlowDocument
	if err := json.Unmarshal([]byte(jsonStr), &vdoc); err != nil {
		return nil, fmt.Errorf("failed to unmarshal VueFlow JSON: %w", err)
	}

	fdoc := vdoc.ToFlowgram()
	def := fdoc.ToDefinition("vueflow_compiled", "Workflow compiled dynamically from VueFlow UI")

	return CompileToCompose(ctx, def, rc)
}
