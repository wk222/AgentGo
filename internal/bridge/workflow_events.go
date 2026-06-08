package bridge

import (
	"github.com/wailsapp/wails/v3/pkg/application"

	"agentgo/internal/workflow"
)

// recordWorkflowEvent persists step records and emits UI events (notify → workflow:notify).
func (r *Runtime) recordWorkflowEvent(e workflow.Event) {
	if r.wfStore != nil {
		switch e.Type {
		case "node_start":
			_ = r.wfStore.SaveStep(workflow.StepRecord{
				ID: e.ID, RunID: e.RunID, NodeID: e.NodeID, NodeType: e.NodeType,
				Status: "running", Input: e.Input, CreatedAt: e.Timestamp, UpdatedAt: e.Timestamp,
			})
		case "node_done":
			_ = r.wfStore.SaveStep(workflow.StepRecord{
				ID: e.ID, RunID: e.RunID, NodeID: e.NodeID, NodeType: e.NodeType,
				Status: "completed", Input: e.Input, Output: e.Output, UpdatedAt: e.Timestamp,
			})
		case "node_error":
			_ = r.wfStore.SaveStep(workflow.StepRecord{
				ID: e.ID, RunID: e.RunID, NodeID: e.NodeID, NodeType: e.NodeType,
				Status: "failed", Input: e.Input, Error: e.Error, UpdatedAt: e.Timestamp,
			})
		}
	}
	if e.Type == "notify" || e.NodeType == "notify" {
		if app := application.Get(); app != nil {
			app.Event.Emit("workflow:notify", map[string]any{
				"run_id":  e.RunID,
				"node_id": e.NodeID,
				"channel": e.Output,
				"message": e.Input,
			})
		}
	}
}
