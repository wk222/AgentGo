package bridge

import (
	"encoding/json"
	"os"
	"strings"

	"agentgo/internal/workflow"
)

func (s *AppService) sseGatewayStatus() map[string]any {
	addr := strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_ADDR"))
	if addr == "" {
		if p := strings.TrimSpace(os.Getenv("AGENTGO_GATEWAY_PORT")); p != "" {
			addr = "127.0.0.1:" + p
		}
	}
	enabled := s.rt.gatewaySrv != nil && addr != ""
	endpoints := []string{}
	if enabled {
		base := "http://" + addr
		endpoints = []string{
			base + "/health",
			base + "/v1/chat/completions",
			base + "/v1/models",
			base + "/api/v1/chat/stream",
			base + "/api/v1/tasks/{id}/events?after_seq=0",
			base + "/api/v1/workflows",
			base + "/api/v1/workflows/{id}/flowgram",
			base + "/api/v1/workflows/{id}/run",
			base + "/api/v1/inner-apps",
			base + "/api/v1/inner-apps/{name}/invoke",
			base + "/api/v1/inner-apps/{name}/assets/{path}",
		}
	}
	return map[string]any{
		"enabled":   enabled,
		"addr":      addr,
		"endpoints": endpoints,
		"note":      "SSE: event chunk|done|error；与 Wails task:event 同源",
	}
}

// GetWorkflowFlowgram loads Coze/Flowgram canvas JSON for the editor.
func (s *AppService) GetWorkflowFlowgram(workflowID string) map[string]any {
	if s.rt.wfStore == nil {
		return map[string]any{"success": false, "error": "no store"}
	}
	doc, err := s.rt.wfStore.GetFlowgram(workflowID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "flowgram": doc}
}

// SaveWorkflowFlowgram persists canvas JSON and syncs executable graph.
func (s *AppService) SaveWorkflowFlowgram(workflowID string, flowgramJSON string) map[string]any {
	if s.rt.wfStore == nil {
		return map[string]any{"success": false}
	}
	var doc workflow.FlowgramDocument
	if flowgramJSON != "" {
		_ = json.Unmarshal([]byte(flowgramJSON), &doc)
	}
	if err := s.rt.wfStore.SaveFlowgram(workflowID, doc); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	_ = s.rt.SyncWorkflowTools()
	return map[string]any{"success": true}
}
