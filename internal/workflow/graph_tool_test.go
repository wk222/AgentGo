package workflow

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

type stubRunner struct {
	out string
}

func (s stubRunner) Run(_ context.Context, _, _ string) (string, error) {
	return s.out, nil
}

func TestRunnerShellGraphTool(t *testing.T) {
	gt, err := NewRunnerShellGraphTool("wf1", "graph_shell_test", "test", stubRunner{out: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := gt.InvokableRun(context.Background(), `{"input":"hi"}`)
	if err != nil || out != "ok" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestNewWorkflowGraphToolSimpleChain(t *testing.T) {
	def := Definition{
		Name: "chain",
		Nodes: []Node{
			{ID: "n1", Type: "input"},
			{ID: "n2", Type: "code", Prompt: "{{input}}"},
			{ID: "n3", Type: "output"},
		},
		Edges: []Edge{
			{From: "n1", To: "n2"},
			{From: "n2", To: "n3"},
		},
	}
	rc := &RunContext{Vars: map[string]string{}}
	gt, err := NewWorkflowGraphTool(context.Background(), def, rc, "graph_test", "test")
	if err != nil {
		t.Fatal(err)
	}
	out, err := gt.InvokableRun(context.Background(), `{"input":"hello"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("expected output")
	}
}


// TestWorkflowGraphToolConcurrentIsolation guards against the per-run state race that
// existed when the tool mutated a shared RunContext (Input/RunID/Vars/signal funcs) on
// every call. Each goroutine must get back exactly its own input, and the static
// template's Vars map must never be written to by a run. Run with -race to catch
// regressions on the shared mutable state.
func TestWorkflowGraphToolConcurrentIsolation(t *testing.T) {
	def := Definition{
		Name: "echo_iso",
		Nodes: []Node{
			{ID: "n1", Type: "input"},
			{ID: "n2", Type: "set_var", Config: map[string]any{"name": "seen", "value": "{{input}}"}},
			{ID: "n3", Type: "code", Prompt: "{{input}}"},
			{ID: "n4", Type: "output"},
		},
		Edges: []Edge{
			{From: "n1", To: "n2"},
			{From: "n2", To: "n3"},
			{From: "n3", To: "n4"},
		},
	}
	rc := &RunContext{Vars: map[string]string{}}
	gt, err := NewWorkflowGraphTool(context.Background(), def, rc, "graph_iso", "test")
	if err != nil {
		t.Fatal(err)
	}

	const n = 16
	var wg sync.WaitGroup
	outs := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf(`{"input":"payload-%d"}`, i)
			outs[i], errs[i] = gt.InvokableRun(context.Background(), args)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("run %d error: %v", i, errs[i])
		}
		want := fmt.Sprintf("payload-%d", i)
		if outs[i] != want {
			t.Fatalf("run %d cross-contaminated: got %q want %q", i, outs[i], want)
		}
	}

	// The template's Vars map must remain pristine — runs operate on private copies.
	if len(rc.Vars) != 0 {
		t.Fatalf("template Vars leaked per-run state: %+v", rc.Vars)
	}
}
