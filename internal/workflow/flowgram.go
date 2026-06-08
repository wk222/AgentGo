package workflow

import (
	"encoding/json"
	"strconv"
	"strings"
)

// FlowgramDocument matches Coze Studio / @flowgram-adapter WorkflowJSON (subset).
type FlowgramDocument struct {
	Nodes []FlowgramNode `json:"nodes"`
	Edges []FlowgramEdge `json:"edges"`
}

type FlowgramNode struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Meta struct {
		Position struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		} `json:"position"`
	} `json:"meta"`
	Data map[string]any `json:"data,omitempty"`
}

type FlowgramEdge struct {
	SourceNodeID string `json:"sourceNodeID"`
	TargetNodeID string `json:"targetNodeID"`
	SourcePortID string `json:"sourcePortID,omitempty"`
	When         string `json:"when,omitempty"`
	Label        string `json:"label,omitempty"`
}

// ToDefinition converts Flowgram canvas JSON to executable Definition.
func (doc FlowgramDocument) ToDefinition(name, description string) Definition {
	def := Definition{
		Name: name, Description: description,
		Meta: map[string]any{"flowgram": true},
	}
	for _, n := range doc.Nodes {
		nt := normalizeNodeType(n.Type)
		node := Node{
			ID: n.ID, Type: nt, Title: n.ID,
			Config: map[string]any{"x": n.Meta.Position.X, "y": n.Meta.Position.Y},
		}
		if node.Config == nil {
			node.Config = map[string]any{}
		}
		if n.Data != nil {
			if p, ok := n.Data["prompt"].(string); ok {
				node.Prompt = p
			}
			if p, ok := n.Data["systemPrompt"].(string); ok && node.Prompt == "" {
				node.Prompt = p
			}
			if c, ok := n.Data["code"].(string); ok && node.Prompt == "" {
				node.Prompt = c
			}
			if t, ok := n.Data["tool_name"].(string); ok {
				node.ToolName = t
			}
			if a, ok := n.Data["args"].(string); ok {
				node.ArgsJSON = a
			}
			if u, ok := n.Data["url"].(string); ok {
				node.Config["url"] = u
			}
			for _, k := range []string{
				"channel", "message", "metric", "threshold", "path", "data",
				"method", "body", "name", "value", "field", "script", "workflow_id",
				"agent_name", "topic", "rounds", "description", "output_var", "branches",
				"collection", "collection_var", "item_var", "max_iterations", "batch_size",
				"merge_strategy", "max_workers", "branch_table", "wait_for_all",
			} {
				if v, ok := n.Data[k]; ok {
					node.Config[k] = v
				}
			}
			ingestBranchTable(&node, n.Data)
			if sc, ok := n.Data["sub_canvas"].(string); ok && strings.TrimSpace(sc) != "" {
				node.Config["sub_flowgram"] = sc
			}
			if expr, ok := n.Data["expression"].(string); ok && expr != "" {
				node.Config["when"] = expr
			}
			if sec, ok := n.Data["seconds"]; ok {
				switch v := sec.(type) {
				case float64:
					node.Config["ms"] = int(v * 1000)
				case string:
					if f, err := parseFloat(v); err == nil {
						node.Config["ms"] = int(f * 1000)
					}
				}
			}
		}
		switch nt {
		case "start":
			node.Title = "开始"
		case "end":
			node.Title = "结束"
		case "llm":
			if node.Prompt == "" {
				node.Prompt = "{{input}}"
			}
		}
		def.Nodes = append(def.Nodes, node)
	}
	for _, e := range doc.Edges {
		edge := Edge{From: e.SourceNodeID, To: e.TargetNodeID}
		w := strings.TrimSpace(e.When)
		if w == "" {
			w = strings.TrimSpace(e.Label)
		}
		if w == "" {
			w = strings.TrimSpace(e.SourcePortID)
		}
		edge.When = w
		def.Edges = append(def.Edges, edge)
	}
	attachParallelBranchFlowgrams(&def, doc)
	attachMergeIncoming(&def)
	if len(def.Nodes) == 0 {
		def.Nodes = []Node{
			{ID: "start", Type: "start", Title: "开始"},
			{ID: "llm1", Type: "llm", Title: "LLM", Prompt: "{{input}}"},
			{ID: "end", Type: "end", Title: "结束"},
		}
		def.Edges = []Edge{{From: "start", To: "llm1"}, {From: "llm1", To: "end"}}
	}
	return def
}

// FromDefinition builds Flowgram JSON for the canvas editor.
func FromDefinition(def Definition) FlowgramDocument {
	if raw, ok := def.Meta["flowgram_json"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			var doc FlowgramDocument
			if json.Unmarshal([]byte(s), &doc) == nil && len(doc.Nodes) > 0 {
				return doc
			}
		}
	}
	doc := FlowgramDocument{}
	x := 80.0
	for _, n := range def.Nodes {
		fn := FlowgramNode{ID: n.ID, Type: flowgramType(n.Type), Data: map[string]any{}}
		if n.Prompt != "" {
			fn.Data["prompt"] = n.Prompt
		}
		if n.ToolName != "" {
			switch n.Type {
			case "subworkflow", "workflow":
				fn.Data["workflow_id"] = n.ToolName
			case "agent", "adk_agent":
				fn.Data["agent_name"] = n.ToolName
			default:
				fn.Data["tool_name"] = n.ToolName
			}
		}
		if n.ArgsJSON != "" {
			fn.Data["args"] = n.ArgsJSON
		}
		if n.Config != nil {
			for _, k := range []string{
				"channel", "message", "metric", "threshold", "path", "data",
				"method", "body", "name", "value", "field", "script", "workflow_id",
				"agent_name", "topic", "rounds", "description", "output_var", "when", "branches",
				"collection", "collection_var", "item_var", "max_iterations", "batch_size",
				"merge_strategy", "max_workers", "branch_table", "wait_for_all",
			} {
				if v, ok := n.Config[k]; ok {
					fn.Data[k] = v
				}
			}
			if rules, ok := n.Config["branch_rules"].([]any); ok && len(rules) > 0 {
				if _, has := fn.Data["branch_table"]; !has {
					b, _ := json.Marshal(rules)
					fn.Data["branch_table"] = string(b)
				}
			}
			if sf, ok := n.Config["sub_flowgram"].(string); ok && strings.TrimSpace(sf) != "" {
				fn.Data["sub_canvas"] = sf
			}
			if w, ok := n.Config["when"].(string); ok && w != "" {
				fn.Data["expression"] = w
			}
			if ms, ok := n.Config["ms"]; ok {
				switch v := ms.(type) {
				case float64:
					fn.Data["seconds"] = v / 1000
				case int:
					fn.Data["seconds"] = float64(v) / 1000
				}
			}
		}
		if n.Type == "code" && n.Prompt != "" {
			fn.Data["code"] = n.Prompt
		}
		fn.Meta.Position.X = x
		fn.Meta.Position.Y = 80
		if n.Config != nil {
			if vx, ok := n.Config["x"].(float64); ok {
				fn.Meta.Position.X = vx
			}
			if vy, ok := n.Config["y"].(float64); ok {
				fn.Meta.Position.Y = vy
			}
		}
		doc.Nodes = append(doc.Nodes, fn)
		x += 220
	}
	for _, e := range def.Edges {
		fe := FlowgramEdge{SourceNodeID: e.From, TargetNodeID: e.To}
		if w := strings.TrimSpace(e.When); w != "" {
			fe.When = w
			fe.Label = w
		}
		doc.Edges = append(doc.Edges, fe)
	}
	return doc
}

func normalizeNodeType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch {
	case strings.Contains(t, "start"):
		return "start"
	case strings.Contains(t, "end"):
		return "end"
	case strings.Contains(t, "llm"), strings.Contains(t, "model"):
		return "llm"
	case strings.Contains(t, "tool"), strings.Contains(t, "plugin"):
		return "tool"
	case strings.Contains(t, "branch"), strings.Contains(t, "switch"):
		return "branch"
	case strings.Contains(t, "notify"), strings.Contains(t, "notification"):
		return "notify"
	case strings.Contains(t, "condition"), t == "if":
		return "condition"
	case strings.Contains(t, "code"), strings.Contains(t, "script"), strings.Contains(t, "transform"):
		return "code"
	case strings.Contains(t, "http"), strings.Contains(t, "request"), strings.Contains(t, "api"):
		return "http"
	case strings.Contains(t, "monitor"), strings.Contains(t, "watch"):
		return "monitor"
	case strings.Contains(t, "data_source"), strings.Contains(t, "datasource"):
		return "data_source"
	case strings.Contains(t, "delay"), strings.Contains(t, "sleep"):
		return "delay"
	case strings.Contains(t, "variable"), strings.Contains(t, "set_var"):
		return "variable"
	case strings.Contains(t, "json"):
		return "json"
	case strings.Contains(t, "subworkflow"), t == "workflow":
		return "subworkflow"
	case strings.Contains(t, "bash"):
		return "bash"
	case strings.Contains(t, "agent"), strings.Contains(t, "adk"):
		return "agent"
	case strings.Contains(t, "ask_user"), strings.Contains(t, "human"), strings.Contains(t, "hitl"):
		return "ask_user"
	case strings.Contains(t, "debate"), strings.Contains(t, "multi_agent"):
		return "debate"
	case strings.Contains(t, "loop"), strings.Contains(t, "iteration"):
		return "loop"
	case strings.Contains(t, "foreach"):
		return "loop"
	case strings.Contains(t, "batch"):
		return "batch"
	case strings.Contains(t, "parallel"):
		return "parallel"
	case strings.Contains(t, "merge"):
		return "merge"
	default:
		if t == "" {
			return "llm"
		}
		return t
	}
}

func flowgramType(t string) string {
	switch t {
	case "start":
		return "Start"
	case "end":
		return "End"
	case "llm":
		return "LLM"
	case "tool":
		return "Tool"
	case "branch", "condition":
		return "Branch"
	case "code":
		return "Code"
	case "http":
		return "HTTP"
	case "notify":
		return "Notify"
	case "monitor":
		return "Monitor"
	case "data_source":
		return "DataSource"
	case "delay":
		return "Delay"
	case "variable":
		return "Variable"
	case "json":
		return "JSON"
	case "subworkflow":
		return "Subworkflow"
	case "bash":
		return "Bash"
	case "agent":
		return "Agent"
	case "ask_user":
		return "AskUser"
	case "debate":
		return "Debate"
	case "loop":
		return "Loop"
	case "batch":
		return "Batch"
	case "parallel":
		return "Parallel"
	case "merge":
		return "Merge"
	case "foreach":
		return "ForEach"
	default:
		return "LLM"
	}
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}
