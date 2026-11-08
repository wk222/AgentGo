package agentpack

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

type textArgs struct {
	Text string `json:"text"`
}

// nodeEngine is an Engine with the evolution tools registered plus two fake backends.
func nodeEngine(t *testing.T) *Engine {
	t.Helper()
	e := newVersionEngine(t)
	if err := RegisterEvolutionTools(e.Reg, e); err != nil {
		t.Fatal(err)
	}
	shout, err := utils.InferTool("shout", "uppercases text", func(_ context.Context, in textArgs) (map[string]any, error) {
		return map[string]any{"shouted": strings.ToUpper(in.Text), "length": len(in.Text)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Reg.AddTool(shout)
	return e
}

func call(t *testing.T, e *Engine, tool string, args any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(args)
	raw, err := e.Reg.InvokeJSON(context.Background(), tool, string(b))
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%s returned %q: %v", tool, raw, err)
	}
	return out
}

func shoutSpec() workflow.NodeSpec {
	return workflow.NodeSpec{
		ID: "shout_text", Description: "uppercases a text and reports its length",
		Inputs:  []workflow.NodeField{{Name: "text"}},
		Outputs: []workflow.NodeField{{Name: "shouted"}, {Name: "length", Type: "integer"}},
		Backend: workflow.NodeBackend{Kind: "tool", Ref: "shout"},
		Tests: []workflow.NodeTest{
			{Name: "basic", Inputs: map[string]any{"text": "hello"}, Expect: workflow.NodeExpect{Outputs: map[string]any{"shouted": "HELLO", "length": 5}}},
		},
	}
}

func ok(t *testing.T, out map[string]any) {
	t.Helper()
	if out["success"] != true {
		t.Fatalf("expected success, got %v", out)
	}
}

func failed(t *testing.T, out map[string]any, contains string) {
	t.Helper()
	msg, _ := out["error"].(string)
	if out["success"] != false || !strings.Contains(msg, contains) {
		t.Fatalf("expected failure containing %q, got %v", contains, out)
	}
}

func TestDefineNodePublishesAndAWorkflowRunsIt(t *testing.T) {
	e := nodeEngine(t)
	out := call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": shoutSpec()})
	ok(t, out)
	if node := out["node"].(map[string]any); node["version"] != 1.0 || node["use_as"] != "type custom:shout_text" {
		t.Fatalf("node = %v", node)
	}
	if tests := out["tests"].([]any); len(tests) != 1 || tests[0].(map[string]any)["passed"] != true {
		t.Fatalf("tests = %v", out["tests"])
	}

	def := workflow.Definition{
		ID: "uses_shout", Name: "uses shout",
		Nodes: []workflow.Node{
			{ID: "s", Type: "start"},
			{ID: "n", Type: "custom:shout_text", Config: map[string]any{"inputs": map[string]any{"text": "{{input}}"}}},
			{ID: "e", Type: "end"},
		},
		Edges: []workflow.Edge{{From: "s", To: "n"}, {From: "n", To: "e"}},
	}
	saved := call(t, e, "save_workflow_definition", map[string]any{"definition": def})
	ok(t, saved)
	// Saving runs nothing; the result names the tool that runs it.
	if how, _ := saved["run_with"].(string); !strings.HasPrefix(how, "workflow_uses_shout ") {
		t.Fatalf("run_with = %q", saved["run_with"])
	}

	rc := e.nodeRunContext("")
	rc.Input = "agent made this"
	got, err := workflow.Execute(context.Background(), def, rc)
	if err != nil || got != `{"length":15,"shouted":"AGENT MADE THIS"}` {
		t.Fatalf("got %q err %v", got, err)
	}

	listed := call(t, e, "list_workflow_nodes", map[string]any{})
	if listed["count"] != 1.0 || !strings.Contains(listed["nodes"].([]any)[0].(map[string]any)["description"].(string), "uppercases") {
		t.Fatalf("list = %v", listed)
	}
	detail := call(t, e, "list_workflow_nodes", map[string]any{"id": "shout_text"})
	if users := detail["used_by"].([]any); len(users) != 1 || users[0] != "uses_shout" {
		t.Fatalf("detail = %v", detail)
	}
	catalog, _ := e.Catalog(context.Background())
	if nodes, _ := catalog["workflow_nodes"].([]map[string]string); len(nodes) != 1 || nodes[0]["id"] != "shout_text" {
		t.Fatalf("catalog = %v", catalog["workflow_nodes"])
	}
}

func TestDefineNodeRefusesWhatCannotWork(t *testing.T) {
	e := nodeEngine(t)
	noTests := shoutSpec()
	noTests.Tests = nil
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": noTests}), "at least one contract test is required")

	badID := shoutSpec()
	badID.ID = "X"
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": badID}), "snake_case")

	ghostTool := shoutSpec()
	ghostTool.Backend.Ref = "no_such_tool"
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": ghostTool}), `tool "no_such_tool" is not registered`)

	ghostWF := shoutSpec()
	ghostWF.Backend = workflow.NodeBackend{Kind: "subworkflow", Ref: "no_such_wf"}
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": ghostWF}), `workflow "no_such_wf" does not exist`)

	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create"}), "spec is required")
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "update", "spec": shoutSpec()}), "does not exist; use action create")
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "explode"}), "unknown action")
	if _, found := e.WF.GetNode("shout_text"); found {
		t.Fatal("a rejected definition must leave nothing behind")
	}
}

func TestFailingContractTestsSaveNothing(t *testing.T) {
	e := nodeEngine(t)
	bad := shoutSpec()
	bad.Tests[0].Expect.Outputs = map[string]any{"shouted": "hello"} // the tool shouts
	out := call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": bad})
	failed(t, out, "contract tests failed; nothing was saved")
	results := out["tests"].([]any)
	if len(results) != 1 || !strings.Contains(results[0].(map[string]any)["error"].(string), `output "shouted" = HELLO, expected hello`) {
		t.Fatalf("the failing test must say what differed: %v", results)
	}
	if _, found := e.WF.GetNode("shout_text"); found {
		t.Fatal("a node whose tests fail must not be saved")
	}
}

func TestNodeVersionsUpdateRollbackRetire(t *testing.T) {
	e := nodeEngine(t)
	ok(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": shoutSpec()}))
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": shoutSpec()}), "already exists (version 1); use action update")

	v2 := shoutSpec()
	v2.Description = "second version"
	out := call(t, e, "define_workflow_node", map[string]any{"action": "update", "spec": v2})
	ok(t, out)
	if out["node"].(map[string]any)["version"] != 2.0 {
		t.Fatalf("out = %v", out)
	}
	detail := call(t, e, "list_workflow_nodes", map[string]any{"id": "shout_text"})
	if v := detail["versions"].([]any); len(v) != 2 {
		t.Fatalf("versions = %v", v)
	}

	rb := call(t, e, "define_workflow_node", map[string]any{"action": "rollback", "id": "shout_text", "version": 1})
	ok(t, rb)
	cur, _ := e.WF.GetNode("shout_text")
	if cur.Version != 3 || cur.Description != shoutSpec().Description {
		t.Fatalf("rollback republishes the old spec as a new version: %+v", cur.NodeSpec)
	}
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "rollback", "id": "shout_text", "version": 3}), "already the current version")
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "rollback", "id": "shout_text", "version": 9}), "existing versions")

	ok(t, call(t, e, "define_workflow_node", map[string]any{"action": "retire", "id": "shout_text"}))
	if got := call(t, e, "list_workflow_nodes", map[string]any{}); got["count"] != 0.0 {
		t.Fatalf("retired nodes are hidden: %v", got)
	}
	if got := call(t, e, "list_workflow_nodes", map[string]any{"include_retired": true}); got["count"] != 1.0 {
		t.Fatalf("include_retired: %v", got)
	}
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": shoutSpec()}), "is retired")
	ok(t, call(t, e, "define_workflow_node", map[string]any{"action": "update", "spec": shoutSpec()}))
	if cur, _ := e.WF.GetNode("shout_text"); cur.Retired || cur.Version != 4 {
		t.Fatalf("update revives a retired node: %+v", cur)
	}
}

func TestTestActionRerunsTheStoredTests(t *testing.T) {
	e := nodeEngine(t)
	ok(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": shoutSpec()}))
	ok(t, call(t, e, "define_workflow_node", map[string]any{"action": "test", "id": "shout_text"}))

	// the world changes behind the node: the tool now behaves differently
	broken, _ := utils.InferTool("shout", "now whispers", func(_ context.Context, in textArgs) (map[string]any, error) {
		return map[string]any{"shouted": strings.ToLower(in.Text), "length": len(in.Text)}, nil
	})
	e.Reg.AddTool(broken)
	out := call(t, e, "define_workflow_node", map[string]any{"action": "test", "id": "shout_text"})
	if out["success"] != false || len(out["tests"].([]any)) != 1 {
		t.Fatalf("the contract must catch a backend that changed: %v", out)
	}
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "test", "id": "ghost_node"}), "does not exist")
}

func TestSubworkflowBackendCannotCloseACycle(t *testing.T) {
	e := nodeEngine(t)
	// loop_wf already contains the node that is about to wrap it.
	loop := workflow.Definition{
		ID: "loop_wf", Name: "loop",
		Nodes: []workflow.Node{{ID: "s", Type: "start"}, {ID: "n", Type: "custom:wrap_loop"}, {ID: "e", Type: "end"}},
		Edges: []workflow.Edge{{From: "s", To: "n"}, {From: "n", To: "e"}},
	}
	if err := e.WF.Save(loop); err != nil {
		t.Fatal(err)
	}
	spec := workflow.NodeSpec{
		ID: "wrap_loop", Description: "wraps loop_wf",
		Backend: workflow.NodeBackend{Kind: "subworkflow", Ref: "loop_wf"},
		Tests:   []workflow.NodeTest{{Name: "t", Expect: workflow.NodeExpect{Contains: "x"}}},
	}
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": spec}), "would make a workflow run itself")
}

func TestSaveWorkflowDefinitionLintsReferences(t *testing.T) {
	e := nodeEngine(t)
	mk := func(nodes ...workflow.Node) workflow.Definition {
		all := append([]workflow.Node{{ID: "s", Type: "start"}}, nodes...)
		all = append(all, workflow.Node{ID: "e", Type: "end"})
		var edges []workflow.Edge
		for i := 0; i+1 < len(all); i++ {
			edges = append(edges, workflow.Edge{From: all[i].ID, To: all[i+1].ID})
		}
		return workflow.Definition{ID: "lint_wf", Name: "lint wf", Nodes: all, Edges: edges}
	}
	save := func(d workflow.Definition) map[string]any {
		return call(t, e, "save_workflow_definition", map[string]any{"definition": d})
	}
	for _, c := range []struct {
		name string
		def  workflow.Definition
		want string
	}{
		{"unknown tool", mk(workflow.Node{ID: "a", Type: "tool", ToolName: "ghost_tool"}), `tool "ghost_tool" is not registered`},
		{"unknown node type", mk(workflow.Node{ID: "a", Type: "teleport"}), `unknown node type "teleport"`},
		{"undefined custom node", mk(workflow.Node{ID: "a", Type: "custom:ghost_node"}), `custom node "ghost_node" is not defined`},
		{"missing subworkflow", mk(workflow.Node{ID: "a", Type: "subworkflow", ToolName: "ghost_wf"}), `workflow "ghost_wf" does not exist`},
		{"self reference", mk(workflow.Node{ID: "a", Type: "subworkflow", ToolName: "lint_wf"}), "workflow cycle: lint_wf -> lint_wf"},
	} {
		out := save(c.def)
		failed(t, out, c.want)
		if !strings.Contains(out["error"].(string), "nothing was saved") {
			t.Errorf("%s: %v", c.name, out["error"])
		}
	}
	if _, err := e.WF.Get("lint_wf"); err == nil {
		t.Fatal("a workflow with errors must not be stored")
	}

	good := save(mk(workflow.Node{ID: "a", Type: "tool", ToolName: "shout", ArgsJSON: `{"text":"{{input}}"}`}))
	ok(t, good)
	if w, _ := good["warnings"].([]any); len(w) != 1 || !strings.Contains(w[0].(map[string]any)["message"].(string), "do not expand") {
		t.Fatalf("a saved workflow still reports warnings: %v", good)
	}
	if _, err := e.WF.Get("lint_wf"); err != nil {
		t.Fatal(err)
	}
}

func havePython() bool {
	for _, n := range []string{"python", "python3"} {
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	return false
}

func pySpec(code string) workflow.NodeSpec {
	return workflow.NodeSpec{
		ID: "py_double", Description: "doubles an integer with Python",
		Inputs:  []workflow.NodeField{{Name: "n", Type: "integer"}},
		Outputs: []workflow.NodeField{{Name: "doubled", Type: "integer"}},
		Backend: workflow.NodeBackend{Kind: "python", Code: code},
		Tests: []workflow.NodeTest{
			{Name: "twenty one", Inputs: map[string]any{"n": 21}, Expect: workflow.NodeExpect{Outputs: map[string]any{"doubled": 42}}},
		},
	}
}

const pyGood = "import json\nprint(json.dumps({'doubled': args['n'] * 2}))\n"
const pyWrong = "import json\nprint(json.dumps({'doubled': args['n'] * 3}))\n"

func TestPythonNodeBecomesAGeneratedToolAndRollsBackOnFailure(t *testing.T) {
	if !havePython() {
		t.Skip("no python interpreter on PATH")
	}
	e := nodeEngine(t)
	var hostCalls atomic.Int32
	e.NodeRunContext = func() *workflow.RunContext {
		return &workflow.RunContext{ToolRunner: workflow.ToolRunnerFunc(func(ctx context.Context, name, args string) (string, error) {
			hostCalls.Add(1)
			return e.Reg.InvokeJSON(ctx, name, args)
		})}
	}
	out := call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": pySpec(pyGood)})
	ok(t, out)
	def, err := e.Dyn.Get("node_py_double")
	if err != nil || !strings.Contains(def.Code, "args['n'] * 2") {
		t.Fatalf("the python code is stored as a generated tool: %+v %v", def, err)
	}
	if !e.Reg.IsDynamicCompiled("node_py_double") {
		t.Fatal("the generated tool must be hot-loaded")
	}
	if hostCalls.Load() != 0 {
		t.Fatalf("the node's own code runs without a second approval, got %d governed calls", hostCalls.Load())
	}

	// a worse version fails its test: nothing changes, the working tool is back
	bad := call(t, e, "define_workflow_node", map[string]any{"action": "update", "spec": pySpec(pyWrong)})
	failed(t, bad, "contract tests failed")
	if def, _ := e.Dyn.Get("node_py_double"); !strings.Contains(def.Code, "* 2") {
		t.Fatalf("the previous code must be restored, got %q", def.Code)
	}
	if cur, _ := e.WF.GetNode("py_double"); cur.Version != 1 {
		t.Fatalf("the manifest must stay at version 1: %+v", cur)
	}
	again := call(t, e, "define_workflow_node", map[string]any{"action": "test", "id": "py_double"})
	ok(t, again)

	// a first-time python node that fails leaves no tool behind
	fresh := pySpec(pyWrong)
	fresh.ID = "py_fresh"
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": fresh}), "contract tests failed")
	if _, err := e.Dyn.Get("node_py_fresh"); err == nil {
		t.Fatal("the stored tool of a failed first attempt must be deleted")
	}
	if _, live := e.Reg.Get("node_py_fresh"); live {
		t.Fatal("the live tool of a failed first attempt must be unregistered")
	}
}

func TestPythonNodeCannotTakeOverAnotherTool(t *testing.T) {
	if !havePython() {
		t.Skip("no python interpreter on PATH")
	}
	e := nodeEngine(t)
	if err := e.Dyn.Save(tools.DynamicToolDef{Name: "node_py_double", Description: "someone else's tool", Parameters: "{}", Code: "print('x')"}); err != nil {
		t.Fatal(err)
	}
	failed(t, call(t, e, "define_workflow_node", map[string]any{"action": "create", "spec": pySpec(pyGood)}), "is not the backend of this node")
	if def, _ := e.Dyn.Get("node_py_double"); def.Description != "someone else's tool" {
		t.Fatal("the other tool must not be overwritten")
	}
}

func TestNodeParametersSchema(t *testing.T) {
	got := nodeParametersSchema([]workflow.NodeField{
		{Name: "city", Description: "where"},
		{Name: "days", Type: "integer", Default: int64(3)},
		{Name: "mode", Enum: []string{"a", "b"}, Optional: true},
		{Name: "extra", Type: "json", Optional: true},
	})
	var s struct {
		Type       string                    `json:"type"`
		Required   []string                  `json:"required"`
		Properties map[string]map[string]any `json:"properties"`
	}
	if err := json.Unmarshal([]byte(got), &s); err != nil {
		t.Fatal(err)
	}
	if s.Type != "object" || len(s.Required) != 1 || s.Required[0] != "city" {
		t.Fatalf("only inputs without default or optional are required: %s", got)
	}
	if s.Properties["days"]["type"] != "integer" || s.Properties["city"]["description"] != "where" || s.Properties["extra"]["type"] != nil {
		t.Fatalf("schema = %s", got)
	}
}
