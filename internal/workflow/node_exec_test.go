package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// nodeTestEnv wires custom nodes to fake tools the way the host wires them to the governed registry.
type nodeTestEnv struct {
	nodes map[string]NodeManifest
	tools map[string]func(ctx context.Context, args string) (string, error)
	calls []string
	subs  map[string]func(in string) (string, error)
}

func newNodeEnv() *nodeTestEnv {
	return &nodeTestEnv{
		nodes: map[string]NodeManifest{},
		tools: map[string]func(context.Context, string) (string, error){},
		subs:  map[string]func(string) (string, error){},
	}
}

func (e *nodeTestEnv) add(t *testing.T, s NodeSpec) NodeManifest {
	t.Helper()
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	m := NodeManifest{NodeSpec: s, Version: 1}
	e.nodes[s.ID] = m
	return m
}

func (e *nodeTestEnv) rc() RunContext {
	return RunContext{
		Vars:        map[string]string{},
		ResolveNode: func(id string) (NodeManifest, bool) { m, ok := e.nodes[id]; return m, ok },
		ToolRunner: ToolRunnerFunc(func(ctx context.Context, name, args string) (string, error) {
			f, ok := e.tools[name]
			if !ok {
				return "", fmt.Errorf("tool %q not found", name)
			}
			e.calls = append(e.calls, name+" "+args)
			return f(ctx, args)
		}),
		RunSubworkflow: func(ctx context.Context, id, in string) (string, error) {
			f, ok := e.subs[id]
			if !ok {
				return "", fmt.Errorf("workflow %q not found", id)
			}
			e.calls = append(e.calls, "workflow:"+id+" "+in)
			return f(in)
		},
	}
}

func oneNode(typ string, cfg map[string]any) Definition {
	return Definition{
		ID: "wf", Name: "wf",
		Nodes: []Node{{ID: "s", Type: "start"}, {ID: "n1", Type: typ, Config: cfg}, {ID: "e", Type: "end"}},
		Edges: []Edge{{From: "s", To: "n1"}, {From: "n1", To: "e"}},
	}
}

func TestCustomNodeRunsToolBackendEndToEnd(t *testing.T) {
	env := newNodeEnv()
	env.add(t, NodeSpec{
		ID: "upper_case", Description: "uppercases text",
		Inputs:  []NodeField{{Name: "text"}},
		Outputs: []NodeField{{Name: "upper"}},
		Backend: NodeBackend{Kind: "tool", Ref: "to_upper"},
	})
	env.tools["to_upper"] = func(_ context.Context, args string) (string, error) {
		var in struct{ Text string }
		_ = json.Unmarshal([]byte(args), &in)
		return strings.ToUpper(in.Text), nil
	}
	rc := env.rc()
	rc.Input = "hello"
	out, err := Execute(context.Background(), oneNode("custom:upper_case", map[string]any{"inputs": map[string]any{"text": "{{input}}"}}), rc)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"upper":"HELLO"}` {
		t.Fatalf("out = %s", out)
	}
	if rc.Vars["n1.upper"] != "HELLO" {
		t.Fatalf("outputs are published as variables, got %v", rc.Vars)
	}
}

func TestCustomNodeOutputsFeedTheNextNodeByKey(t *testing.T) {
	env := newNodeEnv()
	env.add(t, NodeSpec{
		ID: "split_text", Description: "splits",
		Inputs:  []NodeField{{Name: "text"}},
		Outputs: []NodeField{{Name: "first"}, {Name: "count", Type: "integer"}},
		Backend: NodeBackend{Kind: "tool", Ref: "split"},
	})
	env.add(t, NodeSpec{
		ID: "repeat_text", Description: "repeats",
		Inputs:  []NodeField{{Name: "first"}, {Name: "count", Type: "integer"}},
		Backend: NodeBackend{Kind: "tool", Ref: "repeat"},
	})
	env.tools["split"] = func(context.Context, string) (string, error) { return `{"first":"a","count":3}`, nil }
	env.tools["repeat"] = func(_ context.Context, args string) (string, error) {
		var in struct {
			First string
			Count int
		}
		_ = json.Unmarshal([]byte(args), &in)
		return strings.Repeat(in.First, in.Count), nil
	}
	def := Definition{
		ID: "chain", Name: "chain",
		Nodes: []Node{{ID: "s", Type: "start"}, {ID: "a", Type: "custom:split_text"}, {ID: "b", Type: "custom:repeat_text"}, {ID: "e", Type: "end"}},
		Edges: []Edge{{From: "s", To: "a"}, {From: "a", To: "b"}, {From: "b", To: "e"}},
	}
	rc := env.rc()
	rc.Input = "abc"
	out, err := Execute(context.Background(), def, rc)
	if err != nil {
		t.Fatal(err)
	}
	if out != "aaa" {
		t.Fatalf("the second node binds first/count from the previous JSON output, got %q", out)
	}
}

func TestToolArgsTemplateKeepsTypes(t *testing.T) {
	env := newNodeEnv()
	m := env.add(t, NodeSpec{
		ID: "adder", Description: "adds",
		Inputs:  []NodeField{{Name: "a", Type: "number"}, {Name: "b", Type: "number"}},
		Backend: NodeBackend{Kind: "tool", Ref: "calc", Args: map[string]any{"op": "add", "values": []any{"{{in.a}}", "{{in.b}}"}, "label": "sum of {{in.a}} and {{in.b}}"}},
	})
	env.tools["calc"] = func(context.Context, string) (string, error) { return "5", nil }
	if _, err := RunNode(context.Background(), m, map[string]any{"a": 2.0, "b": 3.0}, env.rc()); err != nil {
		t.Fatal(err)
	}
	want := `calc {"label":"sum of 2 and 3","op":"add","values":[2,3]}`
	if len(env.calls) != 1 || env.calls[0] != want {
		t.Fatalf("call = %v\nwant   %s", env.calls, want)
	}
}

func TestInputBindingOrderAndErrors(t *testing.T) {
	m := NodeManifest{NodeSpec: NodeSpec{
		ID: "binder", Description: "d",
		Inputs: []NodeField{
			{Name: "city"},
			{Name: "days", Type: "integer", Default: 3.0},
			{Name: "mode", Enum: []string{"fast", "slow"}, Optional: true},
		},
		Backend: NodeBackend{Kind: "tool", Ref: "t"},
	}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}

	got, err := bindInputs(m, map[string]any{"city": "Paris"}, "", false)
	if err != nil || got["city"] != "Paris" || got["days"] != int64(3) {
		t.Fatalf("explicit + default: %v %v", got, err)
	}
	got, err = bindInputs(m, nil, `{"city":"Rome","days":"5"}`, true)
	if err != nil || got["city"] != "Rome" || got["days"] != int64(5) {
		t.Fatalf("keys of the previous JSON output bind by name and coerce: %v %v", got, err)
	}
	if _, err = bindInputs(m, nil, "", true); err == nil || !strings.Contains(err.Error(), `input "city" is required`) {
		t.Fatalf("a missing required input must be named: %v", err)
	}
	if _, err = bindInputs(m, map[string]any{"city": "x", "mode": "turbo"}, "", false); err == nil || !strings.Contains(err.Error(), "must be one of fast, slow") {
		t.Fatalf("enum violation: %v", err)
	}
	if _, err = bindInputs(m, map[string]any{"city": "x", "days": "many"}, "", false); err == nil || !strings.Contains(err.Error(), `input "days"`) {
		t.Fatalf("type violation: %v", err)
	}
	if _, err = bindInputs(m, map[string]any{"cty": "x"}, "", false); err == nil || !strings.Contains(err.Error(), "declared: city, days, mode") {
		t.Fatalf("a typo'd input must list the declared names: %v", err)
	}

	single := NodeManifest{NodeSpec: NodeSpec{ID: "single_in", Description: "d", Inputs: []NodeField{{Name: "text"}}, Backend: NodeBackend{Kind: "tool", Ref: "t"}}}
	got, err = bindInputs(single, nil, "just text", true)
	if err != nil || got["text"] != "just text" {
		t.Fatalf("a node with one input takes the previous output as is: %v %v", got, err)
	}
}

func TestOutputParsing(t *testing.T) {
	one := NodeManifest{NodeSpec: NodeSpec{Outputs: []NodeField{{Name: "n", Type: "number"}}}}
	res, err := parseOutputs(one, " 42 \n")
	if err != nil || res.Outputs["n"] != 42.0 || res.Text != `{"n":42}` {
		t.Fatalf("a single output takes the whole text: %+v %v", res, err)
	}
	two := NodeManifest{NodeSpec: NodeSpec{Outputs: []NodeField{{Name: "ok", Type: "boolean"}, {Name: "note", Optional: true}}}}
	res, err = parseOutputs(two, "logging...\n{\"ok\": true}")
	if err != nil || res.Outputs["ok"] != true {
		t.Fatalf("the JSON object may be the last line: %+v %v", res, err)
	}
	if _, err = parseOutputs(two, "not json"); err == nil || !strings.Contains(err.Error(), "must return a JSON object") {
		t.Fatalf("got %v", err)
	}
	if _, err = parseOutputs(two, `{"note":"x"}`); err == nil || !strings.Contains(err.Error(), `output "ok" is missing (the backend returned keys: note)`) {
		t.Fatalf("got %v", err)
	}
	if _, err = parseOutputs(two, `{"ok":"maybe"}`); err == nil || !strings.Contains(err.Error(), `output "ok"`) {
		t.Fatalf("got %v", err)
	}
	none := NodeManifest{}
	res, _ = parseOutputs(none, "plain text")
	if res.Text != "plain text" || res.Outputs["result"] != "plain text" {
		t.Fatalf("no declared outputs passes the text through: %+v", res)
	}
}

func TestToolEnvelopesAreUnwrapped(t *testing.T) {
	out, err := unwrapToolResult("node_x", `{"stdout":"{\"n\":4}\n","stderr":"","exit_code":0}`)
	if err != nil || out != `{"n":4}` {
		t.Fatalf("python envelope: %q %v", out, err)
	}
	if _, err = unwrapToolResult("node_x", `{"stdout":"","stderr":"Traceback...\nboom","exit_code":1}`); err == nil || !strings.Contains(err.Error(), "exited with code 1") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("a failed script is a node error with its stderr: %v", err)
	}
	if _, err = unwrapToolResult("node_x", `{"stdout":"","stderr":"","exit_code":0,"error":"python not found"}`); err == nil || !strings.Contains(err.Error(), "python not found") {
		t.Fatalf("got %v", err)
	}
	out, err = unwrapToolResult("workflow_demo", `{"output":"done"}`)
	if err != nil || out != "done" {
		t.Fatalf("workflow envelope: %q %v", out, err)
	}
	if _, err = unwrapToolResult("run_workflow", `{"output":"","error":"workflow cycle"}`); err == nil {
		t.Fatal("a failed workflow is a node error")
	}
	// A tool that merely returns {output: ...} is not a workflow tool: leave it alone.
	out, _ = unwrapToolResult("some_tool", `{"output":"data"}`)
	if out != `{"output":"data"}` {
		t.Fatalf("only workflow tools are unwrapped, got %q", out)
	}
}

func TestPythonBackendPassesArgumentsAsOneJSONText(t *testing.T) {
	env := newNodeEnv()
	m := env.add(t, NodeSpec{
		ID: "py_sum", Description: "sums",
		Inputs:  []NodeField{{Name: "a", Type: "integer"}, {Name: "b", Type: "integer"}},
		Outputs: []NodeField{{Name: "sum", Type: "integer"}},
		Backend: NodeBackend{Kind: "python", Code: "print('x')"},
	})
	env.tools["node_py_sum"] = func(_ context.Context, args string) (string, error) {
		var wrap struct{ Arguments string }
		_ = json.Unmarshal([]byte(args), &wrap)
		var in struct{ A, B int }
		_ = json.Unmarshal([]byte(wrap.Arguments), &in)
		return fmt.Sprintf(`{"stdout":"{\"sum\":%d}\n","stderr":"","exit_code":0}`, in.A+in.B), nil
	}
	res, err := RunNode(context.Background(), m, map[string]any{"a": int64(2), "b": int64(5)}, env.rc())
	if err != nil || res.Outputs["sum"] != int64(7) {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestNodeTimeoutAndOutputLimit(t *testing.T) {
	env := newNodeEnv()
	slow := env.add(t, NodeSpec{ID: "slow_node", Description: "d", Limits: NodeLimits{TimeoutMS: 100}, Backend: NodeBackend{Kind: "tool", Ref: "slow"}})
	env.tools["slow"] = func(ctx context.Context, _ string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
			return "late", nil
		}
	}
	start := time.Now()
	_, err := RunNode(context.Background(), slow, nil, env.rc())
	if err == nil || !strings.Contains(err.Error(), "timed out after 100ms") || time.Since(start) > 3*time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(start))
	}

	big := env.add(t, NodeSpec{ID: "big_node", Description: "d", Limits: NodeLimits{MaxOutputBytes: 1024}, Backend: NodeBackend{Kind: "tool", Ref: "big"}})
	env.tools["big"] = func(context.Context, string) (string, error) { return strings.Repeat("x", 2000), nil }
	if _, err = RunNode(context.Background(), big, nil, env.rc()); err == nil || !strings.Contains(err.Error(), "over the 1024 byte limit") {
		t.Fatalf("got %v", err)
	}

	// The caller's own cancellation is not reported as a node timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = RunNode(ctx, slow, nil, env.rc()); err == nil || strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v", err)
	}
}

func TestSubworkflowBackendInput(t *testing.T) {
	env := newNodeEnv()
	env.subs["answerer"] = func(in string) (string, error) { return "A:" + in, nil }
	tpl := env.add(t, NodeSpec{ID: "ask_one", Description: "d", Inputs: []NodeField{{Name: "question"}}, Backend: NodeBackend{Kind: "subworkflow", Ref: "answerer", InputTemplate: "Q: {{in.question}}"}})
	res, err := RunNode(context.Background(), tpl, map[string]any{"question": "why"}, env.rc())
	if err != nil || res.Text != "A:Q: why" {
		t.Fatalf("template: %+v %v", res, err)
	}
	single := env.add(t, NodeSpec{ID: "ask_two", Description: "d", Inputs: []NodeField{{Name: "question"}}, Backend: NodeBackend{Kind: "subworkflow", Ref: "answerer"}})
	if res, _ = RunNode(context.Background(), single, map[string]any{"question": "how"}, env.rc()); res.Text != "A:how" {
		t.Fatalf("a single input is handed over as is: %q", res.Text)
	}
	multi := env.add(t, NodeSpec{ID: "ask_three", Description: "d", Inputs: []NodeField{{Name: "a"}, {Name: "b", Type: "integer"}}, Backend: NodeBackend{Kind: "subworkflow", Ref: "answerer"}})
	if res, _ = RunNode(context.Background(), multi, map[string]any{"a": "x", "b": int64(2)}, env.rc()); res.Text != `A:{"a":"x","b":2}` {
		t.Fatalf("several inputs go as JSON: %q", res.Text)
	}
}

func TestCustomNodeFailureModes(t *testing.T) {
	env := newNodeEnv()
	env.add(t, NodeSpec{ID: "known_node", Description: "d", Backend: NodeBackend{Kind: "tool", Ref: "t"}})
	retired := env.add(t, NodeSpec{ID: "old_node", Description: "d", Backend: NodeBackend{Kind: "tool", Ref: "t"}})
	retired.Retired = true
	env.nodes["old_node"] = retired
	run := func(def Definition, rc RunContext) error {
		_, err := Execute(context.Background(), def, rc)
		return err
	}
	if err := run(oneNode("custom:ghost_node", nil), env.rc()); err == nil || !strings.Contains(err.Error(), `"ghost_node" is not defined`) {
		t.Fatalf("undefined: %v", err)
	}
	if err := run(oneNode("custom:old_node", nil), env.rc()); err == nil || !strings.Contains(err.Error(), "is retired") {
		t.Fatalf("retired: %v", err)
	}
	bare := env.rc()
	bare.ResolveNode = nil
	if err := run(oneNode("custom:known_node", nil), bare); err == nil || !strings.Contains(err.Error(), "not available in this run") {
		t.Fatalf("no resolver: %v", err)
	}
	if err := run(oneNode("custom", nil), env.rc()); err == nil || !strings.Contains(err.Error(), "no node id") {
		t.Fatalf("no id: %v", err)
	}
	if err := run(oneNode("custom:known_node", map[string]any{"inputs": "{broken"}), env.rc()); err == nil || !strings.Contains(err.Error(), "config.inputs is not a JSON object") {
		t.Fatalf("inputs as JSON text: %v", err)
	}
}

func TestCustomNodeAcceptsInputsAsJSONTextFromTheCanvas(t *testing.T) {
	env := newNodeEnv()
	env.add(t, NodeSpec{ID: "echo_node", Description: "d", Inputs: []NodeField{{Name: "text"}}, Backend: NodeBackend{Kind: "tool", Ref: "echo"}})
	env.tools["echo"] = func(_ context.Context, args string) (string, error) { return args, nil }
	rc := env.rc()
	rc.Input = "hi"
	out, err := Execute(context.Background(), oneNode("custom", map[string]any{"node_id": "echo_node", "inputs": `{"text":"{{input}}!"}`}), rc)
	if err != nil || out != `{"text":"hi!"}` {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

// A custom node backed by a workflow must be covered by the nesting guard:
// otherwise an agent can wrap a workflow in a node that calls the workflow back.
func TestCustomNodeBackedByItsOwnWorkflowIsACycle(t *testing.T) {
	env := newNodeEnv()
	env.add(t, NodeSpec{ID: "loop_back", Description: "d", Backend: NodeBackend{Kind: "subworkflow", Ref: "outer"}})
	defs := map[string]Definition{"outer": {
		ID: "outer", Name: "outer",
		Nodes: []Node{{ID: "s", Type: "start"}, {ID: "n", Type: "custom:loop_back"}, {ID: "e", Type: "end"}},
		Edges: []Edge{{From: "s", To: "n"}, {From: "n", To: "e"}},
	}}
	rc := env.rc()
	rc.RunSubworkflow = func(ctx context.Context, id, in string) (string, error) {
		sub := env.rc()
		sub.RunSubworkflow = rc.RunSubworkflow
		sub.Input = in
		return Execute(ctx, defs[id], sub)
	}
	rc.Input = "go"
	_, err := Execute(context.Background(), defs["outer"], rc)
	if err == nil || !strings.Contains(err.Error(), "workflow cycle") {
		t.Fatalf("got %v", err)
	}
}
