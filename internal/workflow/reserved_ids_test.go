package workflow

import (
	"context"
	"testing"
)

func llmEcho() RunContext {
	return RunContext{LLMGenerate: func(_ context.Context, p string) (string, error) { return "[" + p + "]", nil }}
}

// Eino reserves the graph keys "start" and "end"; workflows still name their
// entry and exit nodes that way (the seeded examples do), so they must run.
func TestWorkflowWithStartAndEndNodeIDsRuns(t *testing.T) {
	def := Definition{
		ID: "seed_shape", Name: "seed shape",
		Nodes: []Node{
			{ID: "start", Type: "start"},
			{ID: "translator", Type: "llm", Prompt: "T: {{input}}"},
			{ID: "end", Type: "end"},
		},
		Edges: []Edge{{From: "start", To: "translator"}, {From: "translator", To: "end"}},
	}
	rc := llmEcho()
	rc.Input = "hi"
	out, err := Execute(context.Background(), def, rc)
	if err != nil {
		t.Fatalf("a workflow whose entry/exit nodes are called start/end must run: %v", err)
	}
	if out != "[T: hi]" {
		t.Fatalf("out = %q, want [T: hi]", out)
	}
}

func TestBranchCanTargetNodeNamedEnd(t *testing.T) {
	def := Definition{
		ID: "branch_end", Name: "branch end",
		Nodes: []Node{
			{ID: "start", Type: "start"},
			{ID: "check", Type: "condition"},
			{ID: "alt", Type: "code", Prompt: "ALT"},
			{ID: "end", Type: "end"},
		},
		Edges: []Edge{
			{From: "start", To: "check"},
			{From: "check", To: "end", When: "contains:go"},
			{From: "check", To: "alt", When: "contains:stop"},
			{From: "alt", To: "end"},
		},
	}
	for input, want := range map[string]string{"go": "go", "stop": "ALT"} {
		rc := RunContext{Input: input}
		out, err := Execute(context.Background(), def, rc)
		if err != nil {
			t.Fatalf("input %q: %v", input, err)
		}
		if out != want {
			t.Fatalf("input %q: out = %q, want %q", input, out, want)
		}
	}
}

// Regression: the branch table used to mark regular targets false, and Eino only
// accepts a branch result whose entry is true, so every branch to a real node failed.
func TestConditionBranchRoutesToRegularNodes(t *testing.T) {
	def := Definition{
		ID: "route", Name: "route",
		Nodes: []Node{
			{ID: "s", Type: "start"},
			{ID: "c", Type: "condition"},
			{ID: "a", Type: "code", Prompt: "A"},
			{ID: "b", Type: "code", Prompt: "B"},
			{ID: "e", Type: "end"},
		},
		Edges: []Edge{
			{From: "s", To: "c"},
			{From: "c", To: "a", When: "contains:alpha"},
			{From: "c", To: "b", When: "contains:beta"},
			{From: "a", To: "e"},
			{From: "b", To: "e"},
		},
	}
	for input, want := range map[string]string{"alpha": "A", "beta": "B"} {
		out, err := Execute(context.Background(), def, RunContext{Input: input})
		if err != nil {
			t.Fatalf("input %q: %v", input, err)
		}
		if out != want {
			t.Fatalf("input %q: out = %q, want %q", input, out, want)
		}
	}
}

// An edge to the literal id "end" with no node of that name has always meant
// "finish here"; aliasing must not change that.
func TestEdgeToMissingEndNodeStillFinishesTheGraph(t *testing.T) {
	def := Definition{
		ID: "implicit_end", Name: "implicit end",
		Nodes: []Node{
			{ID: "s", Type: "start"},
			{ID: "work", Type: "code", Prompt: "done:{{input}}"},
		},
		Edges: []Edge{{From: "s", To: "work"}, {From: "work", To: "end"}},
	}
	out, err := Execute(context.Background(), def, RunContext{Input: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "done:x" {
		t.Fatalf("out = %q, want done:x", out)
	}
}
