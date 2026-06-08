package workflow

import "encoding/json"

const HappyPathWorkflowID = "wf_happy_path"

// HappyPathDefinition is the canonical B1 demo: Start → LLM → Tool → Notify → End.
func HappyPathDefinition() Definition {
	return Definition{
		ID:          HappyPathWorkflowID,
		Name:        "演示 · LLM → 工具 → 通知",
		Description: "阶段 B happy path：总结输入 → get_current_time → 桌面通知（无需改画布即可运行）",
		Nodes: []Node{
			{ID: "start", Type: "start", Title: "开始"},
			{
				ID: "llm1", Type: "llm", Title: "总结输入",
				Prompt: "用一句中文总结用户输入（不超过 80 字）：\n\n{{input}}",
			},
			{
				ID: "tool1", Type: "tool", Title: "获取时间",
				ToolName: "get_current_time", ArgsJSON: "{}",
			},
			{
				ID: "notify1", Type: "notify", Title: "桌面通知",
				Config: map[string]any{
					"channel": "desktop",
					"message": "【AgentGo 工作流】\n用户输入：{{input}}\n摘要：{{last}}\n（上一步为工具输出）",
				},
			},
			{ID: "end", Type: "end", Title: "结束"},
		},
		Edges: []Edge{
			{From: "start", To: "llm1"},
			{From: "llm1", To: "tool1"},
			{From: "tool1", To: "notify1"},
			{From: "notify1", To: "end"},
		},
		Meta: map[string]any{"template": "happy_path"},
	}
}

// HappyPathFlowgram returns canvas JSON for the workflow panel editor.
func HappyPathFlowgram() FlowgramDocument {
	doc := FlowgramDocument{
		Nodes: []FlowgramNode{
			{ID: "start", Type: "Start", Data: map[string]any{}},
			{ID: "llm1", Type: "LLM", Data: map[string]any{"prompt": "用一句中文总结用户输入（不超过 80 字）：\n\n{{input}}"}},
			{ID: "tool1", Type: "Tool", Data: map[string]any{"tool_name": "get_current_time"}},
			{ID: "notify1", Type: "Notify", Data: map[string]any{
				"channel": "desktop",
				"message": "【AgentGo 工作流】\n用户输入：{{input}}\n摘要：{{last}}",
			}},
			{ID: "end", Type: "End", Data: map[string]any{}},
		},
		Edges: []FlowgramEdge{
			{SourceNodeID: "start", TargetNodeID: "llm1"},
			{SourceNodeID: "llm1", TargetNodeID: "tool1"},
			{SourceNodeID: "tool1", TargetNodeID: "notify1"},
			{SourceNodeID: "notify1", TargetNodeID: "end"},
		},
	}
	for i := range doc.Nodes {
		switch doc.Nodes[i].ID {
		case "start":
			doc.Nodes[i].Meta.Position.X, doc.Nodes[i].Meta.Position.Y = 60, 100
		case "llm1":
			doc.Nodes[i].Meta.Position.X, doc.Nodes[i].Meta.Position.Y = 260, 100
		case "tool1":
			doc.Nodes[i].Meta.Position.X, doc.Nodes[i].Meta.Position.Y = 460, 100
		case "notify1":
			doc.Nodes[i].Meta.Position.X, doc.Nodes[i].Meta.Position.Y = 660, 100
		case "end":
			doc.Nodes[i].Meta.Position.X, doc.Nodes[i].Meta.Position.Y = 860, 100
		}
	}
	return doc
}

// EnsureHappyPathTemplate upserts the demo workflow (safe on every startup).
func (s *Store) EnsureHappyPathTemplate() error {
	def := HappyPathDefinition()
	doc := HappyPathFlowgram()
	b, _ := json.Marshal(doc)
	if def.Meta == nil {
		def.Meta = map[string]any{}
	}
	def.Meta["flowgram_json"] = string(b)
	return s.Save(def)
}
