package core

import (
	"context"
	"strings"
	"testing"
)

// A host that has wired nothing must get errors, not nil-pointer panics.
func TestWorkflowRuntimeDegradesWithoutDependencies(t *testing.T) {
	r := &WorkflowRuntime{}
	ctx := context.Background()

	if _, err := r.Execute(ctx, "wf", "in"); err == nil || !strings.Contains(err.Error(), "store unavailable") {
		t.Fatalf("Execute err = %v", err)
	}
	if _, err := r.Resume(ctx, "wf", "cp", "int", ""); err == nil || !strings.Contains(err.Error(), "store unavailable") {
		t.Fatalf("Resume err = %v", err)
	}
	if _, err := r.ToolInvoke(ctx, "anything", "{}"); err == nil || !strings.Contains(err.Error(), "registry unavailable") {
		t.Fatalf("ToolInvoke err = %v", err)
	}
	if r.Governance() != nil {
		t.Fatal("no approval queue means no governance")
	}
	if r.ToolCallbacks() != nil {
		t.Fatal("no bus means no trace callbacks")
	}

	rc := r.RunContext("wf")
	if _, err := rc.ScheduleCron(ctx, "* * * * *", "p", "s"); err == nil || !strings.Contains(err.Error(), "scheduler unavailable") {
		t.Fatalf("ScheduleCron err = %v", err)
	}
	if _, err := rc.LLMGenerate(ctx, "hi"); err == nil {
		t.Fatal("no model wired must be an error")
	}
	if rc.CheckPointStore != nil {
		t.Fatal("no checkpoint store wired")
	}
}

func TestWorkflowRuntimeUsesPlainChatWithoutAKey(t *testing.T) {
	var gotSystem, gotUser string
	r := &WorkflowRuntime{ChatOnce: func(_ context.Context, system, user string) (string, error) {
		gotSystem, gotUser = system, user
		return "plain", nil
	}}
	out, err := r.RunContext("wf").LLMGenerate(context.Background(), "question")
	if err != nil || out != "plain" || gotSystem != "" || gotUser != "question" {
		t.Fatalf("out=%q err=%v system=%q user=%q", out, err, gotSystem, gotUser)
	}
}
