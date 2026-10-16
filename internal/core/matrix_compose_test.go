package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentgo/internal/capability"
)

func TestMatrixRunsFollowUpWorkflowFromRulePlan(t *testing.T) {
	var gotID, gotIn string
	m := &MatrixOrchestrator{
		RunWorkflow: func(_ context.Context, id, in string) (string, error) {
			gotID, gotIn = id, in
			return "wf-output", nil
		},
	}
	st, err := m.Run(context.Background(), capability.Event{
		Type: "workflow.done", Source: "wf1",
		Payload: map[string]string{"output": "result", "next_workflow_id": "wf2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotID != "wf2" || gotIn != "result" || st.Summary != "wf-output" || st.Error != "" {
		t.Fatalf("id=%q in=%q state=%+v", gotID, gotIn, st)
	}
}

func TestMatrixInnerAppAndMissingExecutors(t *testing.T) {
	ev := capability.Event{Type: "app.registered", Payload: map[string]string{"name": "notes"}}

	m := &MatrixOrchestrator{InvokeApp: func(_ context.Context, name, in string) (string, string) {
		if name != "notes" || in != "info" {
			t.Errorf("app call = %q %q", name, in)
		}
		return "app-info", ""
	}}
	if st, err := m.Run(context.Background(), ev); err != nil || st.Summary != "app-info" {
		t.Fatalf("state=%+v err=%v", st, err)
	}

	failing := &MatrixOrchestrator{InvokeApp: func(context.Context, string, string) (string, string) { return "", "boom" }}
	if st, _ := failing.Run(context.Background(), ev); st.Error != "boom" {
		t.Fatalf("app error not surfaced: %+v", st)
	}

	// No executors wired: the failure is reported in the state, not by panicking.
	none := &MatrixOrchestrator{}
	if st, err := none.Run(context.Background(), ev); err != nil || !strings.Contains(st.Error, "unavailable") {
		t.Fatalf("state=%+v err=%v", st, err)
	}
}

func TestMatrixPlanFallsBackToRulesWhenTheModelFails(t *testing.T) {
	m := &MatrixOrchestrator{PlanLLM: func(context.Context, string, string) (string, error) {
		return "", errors.New("provider down")
	}}
	st, err := m.Run(context.Background(), capability.Event{Type: "tool.compiled", Source: "mytool"})
	if err != nil {
		t.Fatal(err)
	}
	// Coordinator action with no runner: the prompt itself becomes the summary.
	if st.PlanAction != "coordinator" || !strings.Contains(st.Summary, "mytool") {
		t.Fatalf("state=%+v", st)
	}
}

func TestMatrixPlanUsesModelJSONWhenAvailable(t *testing.T) {
	m := &MatrixOrchestrator{
		PlanLLM: func(context.Context, string, string) (string, error) {
			return "noise {\"action\":\"invoke_workflow\",\"target\":\"w9\",\"input\":\"go\"} tail", nil
		},
		RunWorkflow: func(_ context.Context, id, in string) (string, error) { return id + ":" + in, nil },
	}
	st, err := m.Run(context.Background(), capability.Event{Type: "workflow.done"})
	if err != nil || st.Summary != "w9:go" {
		t.Fatalf("state=%+v err=%v", st, err)
	}
}
