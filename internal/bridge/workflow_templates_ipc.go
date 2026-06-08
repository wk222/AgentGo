package bridge

import "agentgo/internal/workflow"

// EnsureWorkflowHappyPath upserts the B1 demo workflow (LLM → Tool → Notify).
func (s *AppService) EnsureWorkflowHappyPath() map[string]any {
	if s.rt.wfStore == nil {
		return map[string]any{"success": false, "error": "workflow store unavailable"}
	}
	if err := s.rt.wfStore.EnsureHappyPathTemplate(); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	def, _ := s.rt.wfStore.Get(workflow.HappyPathWorkflowID)
	return map[string]any{
		"success": true,
		"id":      workflow.HappyPathWorkflowID,
		"name":    def.Name,
		"message": "演示工作流已就绪；在工作流面板选择并运行",
	}
}
