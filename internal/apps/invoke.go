package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Runner executes workflow/agent backends for inner apps.
type Runner interface {
	RunWorkflow(ctx context.Context, workflowID, input string) (string, error)
	RunAgentPrompt(ctx context.Context, sessionID, systemPrompt, userInput string) (string, error)
}

// InvokeRequest is the unified entry for UI, gateway, and agent tools.
type InvokeRequest struct {
	Input       string
	Capability  string
	Action      string
	PayloadJSON string
}

// InvokeResult is returned to callers.
type InvokeResult struct {
	Output string         `json:"output,omitempty"`
	Error  string         `json:"error,omitempty"`
	Kind   string         `json:"kind,omitempty"`
	Meta   map[string]any `json:"meta,omitempty"`
}

// Invoke runs an inner app (workflow | agent | ui).
func Invoke(ctx context.Context, app InnerApp, runner Runner, req InvokeRequest) InvokeResult {
	if !app.Enabled {
		return InvokeResult{Error: fmt.Sprintf("app %q is disabled", app.Name)}
	}
	if runner == nil {
		return InvokeResult{Error: "app runner unavailable"}
	}

	cap := strings.TrimSpace(req.Capability)
	if cap != "" && len(app.Exports) > 0 && !exportAllowed(app.Exports, cap) {
		return InvokeResult{Error: fmt.Sprintf("capability %q not in exports %v", cap, app.Exports)}
	}

	switch strings.ToLower(strings.TrimSpace(app.Kind)) {
	case "workflow":
		wfID := app.WorkflowID
		if cap != "" && app.Metadata != nil {
			if alt := app.Metadata["export_workflow:"+cap]; alt != "" {
				wfID = alt
			}
		}
		if strings.TrimSpace(wfID) == "" {
			return InvokeResult{Error: "workflow_id not configured"}
		}
		in := strings.TrimSpace(req.Input)
		if in == "" && req.PayloadJSON != "" {
			in = req.PayloadJSON
		}
		out, err := runner.RunWorkflow(ctx, wfID, in)
		if err != nil {
			return InvokeResult{Error: err.Error(), Kind: "workflow"}
		}
		return InvokeResult{Output: out, Kind: "workflow"}

	case "agent":
		sid := "inner_app:" + app.Name
		prompt := app.SystemPrompt
		if prompt == "" {
			prompt = "You are inner app " + app.Name
		}
		if sp := app.Metadata["system_prompt:"+cap]; sp != "" {
			prompt = sp
		}
		in := strings.TrimSpace(req.Input)
		if in == "" && req.PayloadJSON != "" {
			in = payloadToUserText(req.PayloadJSON)
		}
		out, err := runner.RunAgentPrompt(ctx, sid, prompt, in)
		if err != nil {
			return InvokeResult{Error: err.Error(), Kind: "agent"}
		}
		return InvokeResult{Output: out, Kind: "agent"}

	case "ui":
		return invokeUI(ctx, app, runner, req)

	default:
		return InvokeResult{Error: "unknown app kind: " + app.Kind}
	}
}

func invokeUI(ctx context.Context, app InnerApp, runner Runner, req InvokeRequest) InvokeResult {
	action := strings.TrimSpace(req.Action)
	if action == "" {
		action = strings.TrimSpace(req.Capability)
	}
	if action == "" && req.PayloadJSON != "" {
		var body struct {
			Action  string          `json:"action"`
			Payload json.RawMessage `json:"payload"`
		}
		if json.Unmarshal([]byte(req.PayloadJSON), &body) == nil && body.Action != "" {
			action = body.Action
			if len(body.Payload) > 0 {
				req.PayloadJSON = string(body.Payload)
			}
		}
	}
	if action == "" || action == "info" {
		return InvokeResult{
			Kind: "ui",
			Meta: map[string]any{
				"name": app.Name, "bundle_path": app.BundlePath,
				"exports": app.Exports, "workflow_id": app.WorkflowID,
			},
			Output: app.Description,
		}
	}
	if len(app.Exports) > 0 && !exportAllowed(app.Exports, action) {
		return InvokeResult{Error: fmt.Sprintf("action %q not in exports %v", action, app.Exports), Kind: "ui"}
	}

	in := strings.TrimSpace(req.Input)
	if in == "" {
		in = payloadToUserText(req.PayloadJSON)
	}

	switch action {
	case "workflow_run", "run":
		wfID := app.WorkflowID
		if app.Metadata != nil {
			if v := app.Metadata["action_workflow:"+action]; v != "" {
				wfID = v
			}
		}
		if strings.TrimSpace(wfID) == "" {
			return InvokeResult{Error: "workflow_run: no workflow_id on app", Kind: "ui"}
		}
		out, err := runner.RunWorkflow(ctx, wfID, in)
		if err != nil {
			return InvokeResult{Error: err.Error(), Kind: "ui"}
		}
		return InvokeResult{Output: out, Kind: "ui", Meta: map[string]any{"action": action, "workflow_id": wfID}}
	case "ping":
		b, _ := json.Marshal(map[string]any{"pong": true, "app": app.Name, "ts": time.Now().Unix()})
		return InvokeResult{Output: string(b), Kind: "ui", Meta: map[string]any{"action": action}}
	case "echo":
		out := payloadToUserText(req.PayloadJSON)
		if out == "" {
			out = strings.TrimSpace(req.Input)
		}
		return InvokeResult{Output: out, Kind: "ui", Meta: map[string]any{"action": action}}
	}

	// UI API: run bound workflow or agent for action
	wfID := app.WorkflowID
	if app.Metadata != nil {
		if v := app.Metadata["action_workflow:"+action]; v != "" {
			wfID = v
		} else if v := app.Metadata["export_workflow:"+action]; v != "" {
			wfID = v
		}
	}
	if in == "" {
		in = action
	}

	if strings.TrimSpace(wfID) != "" {
		out, err := runner.RunWorkflow(ctx, wfID, in)
		if err != nil {
			return InvokeResult{Error: err.Error(), Kind: "ui"}
		}
		return InvokeResult{Output: out, Kind: "ui", Meta: map[string]any{"action": action}}
	}
	if strings.TrimSpace(app.SystemPrompt) != "" {
		sid := "inner_app:" + app.Name
		out, err := runner.RunAgentPrompt(ctx, sid, app.SystemPrompt, in)
		if err != nil {
			return InvokeResult{Error: err.Error(), Kind: "ui"}
		}
		return InvokeResult{Output: out, Kind: "ui", Meta: map[string]any{"action": action}}
	}
	return InvokeResult{
		Error:  fmt.Sprintf("ui action %q has no workflow_id or system_prompt binding", action),
		Kind:   "ui",
		Meta:   map[string]any{"action": action},
		Output: "static ui only — bind workflow_id or system_prompt on the app",
	}
}

func exportAllowed(exports []string, cap string) bool {
	for _, e := range exports {
		if strings.EqualFold(strings.TrimSpace(e), cap) {
			return true
		}
	}
	return false
}

func payloadToUserText(payloadJSON string) string {
	payloadJSON = strings.TrimSpace(payloadJSON)
	if payloadJSON == "" {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(payloadJSON), &m) != nil {
		return payloadJSON
	}
	b, _ := json.Marshal(m)
	return string(b)
}
