package bridge

import "agentgo/internal/workflow"

func (s *AppService) ListWorkflowRuns(workflowID string, limit int) []workflow.RunRecord {
	if s.rt.wfStore == nil {
		return nil
	}
	list, _ := s.rt.wfStore.ListRuns(workflowID, limit)
	return list
}

func (s *AppService) GetWorkflowRun(runID string) map[string]any {
	if s.rt.wfStore == nil {
		return map[string]any{"success": false, "error": "workflow store unavailable"}
	}
	run, err := s.rt.wfStore.GetRun(runID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	steps, _ := s.rt.wfStore.GetSteps(runID)
	return map[string]any{
		"success": true,
		"run":     run,
		"steps":   steps,
	}
}
