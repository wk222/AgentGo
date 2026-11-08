package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// chainDef builds start -> subworkflow(calls[0]) -> subworkflow(calls[1]) ... -> end.
func chainDef(id string, calls ...string) Definition {
	nodes := []Node{{ID: "start", Type: "start"}}
	var edges []Edge
	prev := "start"
	for i, c := range calls {
		nid := fmt.Sprintf("sub%d", i)
		nodes = append(nodes, Node{ID: nid, Type: "subworkflow", ToolName: c})
		edges = append(edges, Edge{From: prev, To: nid})
		prev = nid
	}
	nodes = append(nodes, Node{ID: "end", Type: "end"})
	edges = append(edges, Edge{From: prev, To: "end"})
	return Definition{ID: id, Name: id, Nodes: nodes, Edges: edges}
}

// nestedRunContext lets subworkflow nodes run the other definitions through Execute,
// the same way the host's WorkflowRuntime does.
func nestedRunContext(defs map[string]Definition, input string) RunContext {
	rc := &RunContext{Input: input}
	rc.RunSubworkflow = func(ctx context.Context, id, in string) (string, error) {
		def, ok := defs[id]
		if !ok {
			return "", fmt.Errorf("unknown workflow %s", id)
		}
		sub := *rc
		sub.Input = in
		sub.Vars = nil
		return Execute(ctx, def, sub)
	}
	return *rc
}

func TestEnterWorkflowRefusesCycle(t *testing.T) {
	ctx, err := EnterWorkflow(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = EnterWorkflow(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(CallChain(ctx), ","); got != "a,b" {
		t.Fatalf("chain = %q, want a,b", got)
	}
	_, err = EnterWorkflow(ctx, "a")
	if err == nil || !strings.Contains(err.Error(), "workflow cycle: a -> b -> a") {
		t.Fatalf("want cycle error naming the path, got %v", err)
	}
}

func TestEnterWorkflowIgnoresEmptyID(t *testing.T) {
	ctx, err := EnterWorkflow(context.Background(), "  ")
	if err != nil || len(CallChain(ctx)) != 0 {
		t.Fatalf("empty id must be a no-op, chain=%v err=%v", CallChain(ctx), err)
	}
}

func TestEnterWorkflowDoesNotLeakIntoParentContext(t *testing.T) {
	parent, _ := EnterWorkflow(context.Background(), "a")
	if _, err := EnterWorkflow(parent, "b"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(CallChain(parent), ","); got != "a" {
		t.Fatalf("parent chain mutated: %q", got)
	}
}

func TestExecuteRefusesWorkflowCycle(t *testing.T) {
	defs := map[string]Definition{
		"a": chainDef("a", "b"),
		"b": chainDef("b", "a"),
	}
	_, err := Execute(context.Background(), defs["a"], nestedRunContext(defs, "hi"))
	if err == nil || !strings.Contains(err.Error(), "workflow cycle") {
		t.Fatalf("a -> b -> a must be refused as a cycle, got %v", err)
	}
}

func TestExecuteRefusesSelfReference(t *testing.T) {
	defs := map[string]Definition{"a": chainDef("a", "a")}
	_, err := Execute(context.Background(), defs["a"], nestedRunContext(defs, "hi"))
	if err == nil || !strings.Contains(err.Error(), "workflow cycle") {
		t.Fatalf("a -> a must be refused, got %v", err)
	}
}

func TestExecuteAllowsSameSubworkflowTwiceInSequence(t *testing.T) {
	defs := map[string]Definition{
		"top":  chainDef("top", "leaf", "leaf"),
		"leaf": chainDef("leaf"),
	}
	out, err := Execute(context.Background(), defs["top"], nestedRunContext(defs, "payload"))
	if err != nil {
		t.Fatalf("calling the same workflow twice one after another is not a cycle: %v", err)
	}
	if out != "payload" {
		t.Fatalf("out = %q, want payload", out)
	}
}

func TestExecuteRefusesTooDeepNesting(t *testing.T) {
	t.Setenv("AGENTGO_WORKFLOW_MAX_DEPTH", "3")
	defs := map[string]Definition{
		"w1": chainDef("w1", "w2"),
		"w2": chainDef("w2", "w3"),
		"w3": chainDef("w3", "w4"),
		"w4": chainDef("w4"),
	}
	_, err := Execute(context.Background(), defs["w1"], nestedRunContext(defs, "x"))
	if err == nil || !strings.Contains(err.Error(), "nested too deep (limit 3)") {
		t.Fatalf("4 levels must exceed a limit of 3, got %v", err)
	}
	t.Setenv("AGENTGO_WORKFLOW_MAX_DEPTH", "4")
	if _, err := Execute(context.Background(), defs["w1"], nestedRunContext(defs, "x")); err != nil {
		t.Fatalf("4 levels fit a limit of 4: %v", err)
	}
}

func TestMaxWorkflowDepthFallsBackOnNonsense(t *testing.T) {
	for _, v := range []string{"0", "-2", "999", "abc"} {
		t.Setenv("AGENTGO_WORKFLOW_MAX_DEPTH", v)
		if got := MaxWorkflowDepth(); got != 6 {
			t.Fatalf("AGENTGO_WORKFLOW_MAX_DEPTH=%q -> %d, want default 6", v, got)
		}
	}
}
