package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCodingAgentNode(t *testing.T) {
	var got CodingRequest
	ex := &CodingAgentNodeExecutor{Run: func(_ context.Context, req CodingRequest) (CodingResult, error) {
		got = req
		return CodingResult{Summary: "fixed", Engine: "crush", ToolCalls: 2,
			ChangedFiles: []CodingChange{{Path: "a.go", Before: "x", After: "y"}}}, nil
	}}
	rc := RunContext{RunID: "r1", Vars: map[string]string{}}
	n := Node{ID: "fix", Type: "coding_agent", Prompt: "fix: {{input}}",
		Config: map[string]any{"work_dir": "C:/proj", "engine": "crush"}}
	out, err := ex.Execute(context.Background(), n, "the bug", "", rc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Prompt != "fix: the bug" || got.WorkDir != "C:/proj" || got.Engine != "crush" || got.SessionID != "workflow:r1:fix" {
		t.Fatalf("request = %+v", got)
	}
	var res CodingResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Summary != "fixed" || len(res.ChangedFiles) != 1 {
		t.Fatalf("output = %s (%v)", out, err)
	}
	if rc.Vars["fix.summary"] != "fixed" {
		t.Fatalf("vars = %v", rc.Vars)
	}
}

func TestCodingAgentNodeErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := (&CodingAgentNodeExecutor{}).Execute(ctx, Node{ID: "a", Prompt: "x"}, "", "", RunContext{}); err == nil || !strings.Contains(err.Error(), "no coding engine") {
		t.Fatalf("missing engine err = %v", err)
	}
	ex := &CodingAgentNodeExecutor{Run: func(context.Context, CodingRequest) (CodingResult, error) { return CodingResult{}, errors.New("boom") }}
	if _, err := ex.Execute(ctx, Node{ID: "a", Prompt: "x"}, "", "", RunContext{}); err == nil || err.Error() != "boom" {
		t.Fatalf("engine err = %v", err)
	}
	if _, err := ex.Execute(ctx, Node{ID: "a"}, "", "", RunContext{}); err == nil || !strings.Contains(err.Error(), "empty task") {
		t.Fatalf("empty task err = %v", err)
	}
}

func TestCodingAgentIsNotBuiltInAndFlowgramMapped(t *testing.T) {
	if NodeTypeAvailable("coding_agent") {
		t.Fatal("coding_agent must come from a plugin, not the core")
	}
	if flowgramType("coding_agent") != "CodingAgent" {
		t.Fatalf("flowgramType = %s", flowgramType("coding_agent"))
	}
}

type constExec struct{ out string }

func (c constExec) Execute(context.Context, Node, string, string, RunContext) (string, error) {
	return c.out, nil
}

func TestRegisterNodeExecutorLifecycle(t *testing.T) {
	dispose, err := RegisterNodeExecutor("Ext_Test", constExec{"v1"})
	if err != nil {
		t.Fatal(err)
	}
	if !NodeTypeAvailable("ext_test") {
		t.Fatal("contributed type should be available (case-insensitive)")
	}
	if _, err := RegisterNodeExecutor("ext_test", constExec{"v2"}); err == nil {
		t.Fatal("duplicate contribution accepted")
	}
	if _, err := RegisterNodeExecutor("llm", constExec{"x"}); err == nil || !strings.Contains(err.Error(), "built in") {
		t.Fatalf("shadowing a built-in must fail: %v", err)
	}
	if _, err := RegisterNodeExecutor("", constExec{}); err == nil {
		t.Fatal("empty type accepted")
	}
	dispose()
	dispose() // idempotent
	if NodeTypeAvailable("ext_test") {
		t.Fatal("type must disappear after dispose")
	}
	// a stale dispose must not remove a newer contribution
	d1, _ := RegisterNodeExecutor("ext_test", constExec{"a"})
	d1()
	d2, _ := RegisterNodeExecutor("ext_test", constExec{"b"})
	d1()
	if !NodeTypeAvailable("ext_test") {
		t.Fatal("stale dispose removed the replacement")
	}
	d2()
}
