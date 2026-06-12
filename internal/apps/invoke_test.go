package apps

import (
	"context"
	"testing"
)

type fakeInnerAppRunner struct{}

func (fakeInnerAppRunner) RunWorkflow(context.Context, string, string) (string, error) {
	return "workflow ok", nil
}

func (fakeInnerAppRunner) RunAgentPrompt(context.Context, string, string, string) (string, error) {
	return "agent ok", nil
}

func TestInvokeUIRejectsUnexportedAction(t *testing.T) {
	app := InnerApp{
		Name:       "demo",
		Kind:       "ui",
		Enabled:    true,
		WorkflowID: "wf1",
		Exports:    []string{"ping"},
	}
	res := Invoke(context.Background(), app, fakeInnerAppRunner{}, InvokeRequest{Action: "workflow_run"})
	if res.Error == "" {
		t.Fatalf("expected unexported action to be rejected: %+v", res)
	}
}

func TestInvokeUIAllowsExportedAction(t *testing.T) {
	app := InnerApp{
		Name:       "demo",
		Kind:       "ui",
		Enabled:    true,
		WorkflowID: "wf1",
		Exports:    []string{"workflow_run"},
	}
	res := Invoke(context.Background(), app, fakeInnerAppRunner{}, InvokeRequest{Action: "workflow_run"})
	if res.Error != "" || res.Output != "workflow ok" {
		t.Fatalf("expected exported action to run: %+v", res)
	}
}
