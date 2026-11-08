package agentpack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"

	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

// contractTestBudget caps one whole contract-test run (each test is also bounded by the node's own timeout).
const contractTestBudget = 5 * time.Minute

type defineNodeInput struct {
	Action  string             `json:"action" jsonschema:"description=create or update or test or retire or rollback"`
	Spec    *workflow.NodeSpec `json:"spec,omitempty" jsonschema:"description=create and update: the full node definition"`
	ID      string             `json:"id,omitempty" jsonschema:"description=test and retire and rollback: the node id"`
	Version int                `json:"version,omitempty" jsonschema:"description=rollback: the version to restore (list_workflow_nodes with id lists them)"`
}

type listNodesInput struct {
	ID             string `json:"id,omitempty" jsonschema:"description=show one node in full: definition and code and versions and the workflows that use it"`
	Query          string `json:"query,omitempty" jsonschema:"description=keep nodes whose id or name or description or category contains this text"`
	IncludeRetired bool   `json:"include_retired,omitempty"`
}

func nodeFail(format string, a ...any) map[string]any {
	return map[string]any{"success": false, "error": fmt.Sprintf(format, a...)}
}

func registerNodeTools(reg *tools.Registry, e *Engine) error {
	define, err := utils.InferTool("define_workflow_node",
		"Create or change a reusable workflow node with a typed input/output contract. Anything that takes inputs and returns outputs can be a node, however complex: "+
			"a registered tool (backend.kind tool; ref = tool name; MCP tools and external agents and invoke_inner_app included), "+
			"a whole workflow (kind subworkflow; ref = workflow id), or a Python script (kind python; the code reads the dict args and prints ONE JSON object holding the declared outputs). "+
			"Workflows then use it as node type custom:<id> and map values with config.inputs, e.g. {\"city\":\"{{input}}\"}; an input left out is read by name from the previous node's JSON output. "+
			"Publishing REQUIRES at least one contract test. Tests really run the backend and the node is saved only when every test passes. "+
			"action create = new id; update = new version of an existing id (workflows that use it pick it up on their next run); test = re-run the stored tests; retire; rollback = republish an older version. "+
			"Look at list_workflow_nodes first so existing nodes are reused.",
		func(ctx context.Context, in defineNodeInput) (map[string]any, error) {
			return e.DefineNode(ctx, in), nil
		})
	if err != nil {
		return err
	}
	reg.AddTool(define)
	list, err := utils.InferTool("list_workflow_nodes",
		"List the reusable custom workflow nodes: id and what it does and typed inputs and outputs and backend and version. Pass id for the full definition including code and version history and the workflows that use it. Use a node in a workflow as type custom:<id>.",
		func(ctx context.Context, in listNodesInput) (map[string]any, error) {
			return e.ListNodes(in), nil
		})
	if err != nil {
		return err
	}
	reg.AddTool(list)
	return nil
}

// lintEnv tells workflow.Lint what exists in this installation.
func (e *Engine) lintEnv() workflow.LintEnv {
	var env workflow.LintEnv
	if e.Reg != nil {
		env.ToolExists = func(name string) bool { _, ok := e.Reg.Get(name); return ok }
	}
	if e.WF != nil {
		env.Workflow = func(id string) (workflow.Definition, bool) {
			d, err := e.WF.Get(id)
			return d, err == nil
		}
		env.Node = e.WF.GetNode
	}
	return env
}

// DefineNode implements define_workflow_node.
func (e *Engine) DefineNode(ctx context.Context, in defineNodeInput) map[string]any {
	if e.WF == nil {
		return nodeFail("workflow store unavailable")
	}
	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "create", "update":
		return e.publishNode(ctx, action, in.Spec)
	case "test":
		return e.testStoredNode(ctx, in.ID)
	case "retire":
		return e.retireNode(in.ID)
	case "rollback":
		return e.rollbackNode(ctx, in.ID, in.Version)
	}
	return nodeFail("unknown action %q; use create, update, test, retire or rollback", in.Action)
}

func (e *Engine) publishNode(ctx context.Context, action string, in *workflow.NodeSpec) map[string]any {
	if in == nil {
		return nodeFail("spec is required for %s", action)
	}
	spec := *in
	if err := spec.Normalize(); err != nil {
		return nodeFail("%v", err)
	}
	prev, exists := e.WF.GetNode(spec.ID)
	switch {
	case action == "create" && exists && prev.Retired:
		return nodeFail("node %q is retired (version %d); use action update to revive it", spec.ID, prev.Version)
	case action == "create" && exists:
		return nodeFail("node %q already exists (version %d); use action update to change it", spec.ID, prev.Version)
	case action == "update" && !exists:
		return nodeFail("node %q does not exist; use action create", spec.ID)
	}
	if len(spec.Tests) == 0 {
		return nodeFail("at least one contract test is required: add tests with realistic inputs and what the result must contain or equal (a negative test uses expect.error_contains)")
	}
	if err := e.checkBackend(spec, exists); err != nil {
		return nodeFail("%v", err)
	}
	return e.commitNode(ctx, spec, prev, exists)
}

// checkBackend rejects what can be known to be wrong before anything is installed.
func (e *Engine) checkBackend(spec workflow.NodeSpec, nodeExists bool) error {
	switch spec.Backend.Kind {
	case workflow.BackendTool:
		if e.Reg != nil {
			if _, ok := e.Reg.Get(spec.Backend.Ref); !ok {
				return fmt.Errorf("tool %q is not registered (find the exact name with tool_search)", spec.Backend.Ref)
			}
		}
	case workflow.BackendSubworkflow:
		def, err := e.WF.Get(spec.Backend.Ref)
		if err != nil {
			return fmt.Errorf("workflow %q does not exist (create it first)", spec.Backend.Ref)
		}
		// The workflow behind the node must not be able to reach the node again.
		env := e.lintEnv()
		stored := env.Node
		env.Node = func(id string) (workflow.NodeManifest, bool) {
			if id == spec.ID {
				return workflow.NodeManifest{NodeSpec: spec}, true
			}
			return stored(id)
		}
		for _, is := range workflow.LintErrors(workflow.Lint(def, env)) {
			if strings.HasPrefix(is.Message, "workflow cycle") || strings.HasPrefix(is.Message, "workflows nest") {
				return fmt.Errorf("this backend would make a workflow run itself: %s", is.Message)
			}
		}
	case workflow.BackendPython:
		if e.Dyn == nil || e.Reg == nil {
			return fmt.Errorf("python nodes need the generated-tool store and registry, which are unavailable here")
		}
		name := spec.Backend.Ref
		if _, err := e.Dyn.Get(name); err == nil && !nodeExists {
			return fmt.Errorf("a generated tool named %q already exists and is not the backend of this node; pick another node id", name)
		}
		if _, taken := e.Reg.Get(name); taken && !e.Reg.IsDynamicCompiled(name) {
			return fmt.Errorf("a built-in tool named %q already exists; pick another node id", name)
		}
	}
	return nil
}

// commitNode installs the backend, runs the contract tests and, only if every
// test passes, saves the manifest as the next version. Anything installed for a
// failed attempt is removed again.
func (e *Engine) commitNode(ctx context.Context, spec workflow.NodeSpec, prev workflow.NodeManifest, existed bool) map[string]any {
	ownTool := ""
	var undo func()
	if spec.Backend.Kind == workflow.BackendPython {
		u, err := e.installPythonTool(ctx, spec)
		if err != nil {
			return nodeFail("%v", err)
		}
		ownTool, undo = spec.Backend.Ref, u
	}
	candidate := workflow.NodeManifest{NodeSpec: spec, Version: prev.Version + 1}
	results := e.runNodeTests(ctx, candidate, ownTool)
	if !workflow.AllPassed(results) {
		if undo != nil {
			undo()
		}
		out := nodeFail("contract tests failed; nothing was saved (the stored node is unchanged)")
		out["tests"] = results
		return out
	}
	saved, err := e.WF.SaveNode(spec)
	if err != nil {
		if undo != nil {
			undo()
		}
		return nodeFail("%v", err)
	}
	out := map[string]any{
		"success":    true,
		"node":       saved.Summary(),
		"tests":      results,
		"how_to_use": "workflow node type custom:" + saved.ID + "; set config.inputs per input (a value or a template like {{input}} {{last}} {{var.x}}), or leave an input out to read it by name from the previous node's JSON output",
		"message":    fmt.Sprintf("node %q version %d published; %d contract tests passed", saved.ID, saved.Version, len(results)),
	}
	if users := e.WF.NodeUsage(saved.ID); existed && len(users) > 0 {
		out["used_by"] = users
		out["note"] = "these workflows use the node and pick up this version on their next run; run_workflow one of them to confirm"
	}
	if warn := nodeWarnings(spec); len(warn) > 0 {
		out["warnings"] = warn
	}
	return out
}

func nodeWarnings(spec workflow.NodeSpec) []string {
	var w []string
	if len(spec.Outputs) == 0 {
		w = append(w, "no outputs are declared: the node passes the backend text through unchanged and a following node cannot read fields by name; declare outputs for a typed contract")
	}
	checks := false
	for _, t := range spec.Tests {
		if t.Expect.Contains != "" || len(t.Expect.Outputs) > 0 || t.Expect.ErrorContains != "" {
			checks = true
		}
	}
	if !checks && len(spec.Outputs) == 0 {
		w = append(w, "no test checks an expected value (expect.contains or expect.outputs), so the tests only prove the backend runs")
	}
	return w
}

// installPythonTool turns the python backend into a generated tool and hot-loads
// it. The returned func puts the previous state back.
func (e *Engine) installPythonTool(ctx context.Context, spec workflow.NodeSpec) (func(), error) {
	name := spec.Backend.Ref
	def := tools.DynamicToolDef{
		Name:        name,
		Description: "Python backend of the workflow node custom:" + spec.ID + ": " + spec.Description,
		Parameters:  nodeParametersSchema(spec.Inputs),
		Code:        spec.Backend.Code,
		UsageGuide:  "Call the workflow node custom:" + spec.ID + " instead of this tool.",
		EnvVars:     spec.Backend.EnvVars,
	}
	if err := tools.ValidateDynamicToolDef(ctx, def); err != nil {
		return nil, fmt.Errorf("python backend rejected: %w", err)
	}
	prev, prevErr := e.Dyn.Get(name)
	hadPrev := prevErr == nil
	undo := func() {
		if hadPrev {
			_ = e.Dyn.Save(prev)
			_ = e.hotLoad(prev)
			return
		}
		_ = e.Dyn.Delete(name)
		e.Reg.RemoveDynamicTool(name)
	}
	if err := e.Dyn.Save(def); err != nil {
		return nil, err
	}
	if err := e.hotLoad(def); err != nil {
		undo()
		return nil, fmt.Errorf("python backend could not be registered: %w", err)
	}
	return undo, nil
}

func (e *Engine) hotLoad(def tools.DynamicToolDef) error {
	if e.HotLoadTool != nil {
		return e.HotLoadTool(def)
	}
	if e.Reg == nil {
		return fmt.Errorf("tool registry unavailable")
	}
	return e.Reg.RegisterDynamicTool(def)
}

// nodeParametersSchema renders the declared inputs as the JSON schema a generated tool stores.
func nodeParametersSchema(inputs []workflow.NodeField) string {
	props := map[string]any{}
	required := []string{}
	for _, f := range inputs {
		p := map[string]any{}
		if f.Type != workflow.FieldJSON {
			p["type"] = f.Type
		}
		if f.Description != "" {
			p["description"] = f.Description
		}
		if len(f.Enum) > 0 {
			p["enum"] = f.Enum
		}
		props[f.Name] = p
		if f.Required() {
			required = append(required, f.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, _ := json.Marshal(schema)
	return string(b)
}

// nodeRunContext is the context contract tests run in: the host's governed
// workflow context. The node's own generated tool is the one exception: its code
// is exactly what the caller just approved by calling define_workflow_node.
func (e *Engine) nodeRunContext(ownTool string) workflow.RunContext {
	var rc workflow.RunContext
	if e.NodeRunContext != nil {
		if p := e.NodeRunContext(); p != nil {
			rc = *p
		}
	}
	if rc.ToolRunner == nil && e.Reg != nil {
		rc.ToolRunner = workflow.ToolRunnerFunc(e.Reg.InvokeJSON)
	}
	if rc.ResolveNode == nil && e.WF != nil {
		rc.ResolveNode = e.WF.GetNode
	}
	if ownTool != "" && e.Reg != nil {
		host := rc.ToolRunner
		rc.ToolRunner = workflow.ToolRunnerFunc(func(ctx context.Context, name, args string) (string, error) {
			if name == ownTool || host == nil {
				return e.Reg.InvokeJSON(ctx, name, args)
			}
			return host.Invoke(ctx, name, args)
		})
	}
	return rc
}

func (e *Engine) runNodeTests(ctx context.Context, m workflow.NodeManifest, ownTool string) []workflow.TestResult {
	ctx, cancel := context.WithTimeout(ctx, contractTestBudget)
	defer cancel()
	return workflow.RunContractTests(ctx, m, e.nodeRunContext(ownTool))
}

func (e *Engine) testStoredNode(ctx context.Context, id string) map[string]any {
	id = strings.ToLower(strings.TrimSpace(id))
	m, ok := e.WF.GetNode(id)
	if !ok {
		return nodeFail("node %q does not exist", id)
	}
	if len(m.Tests) == 0 {
		return nodeFail("node %q has no contract tests; publish a new version with tests (action update)", id)
	}
	own := ""
	if m.Backend.Kind == workflow.BackendPython {
		own = m.Backend.Ref
	}
	results := e.runNodeTests(ctx, m, own)
	return map[string]any{"success": workflow.AllPassed(results), "node": m.Summary(), "tests": results}
}

func (e *Engine) retireNode(id string) map[string]any {
	id = strings.ToLower(strings.TrimSpace(id))
	if err := e.WF.RetireNode(id); err != nil {
		return nodeFail("%v", err)
	}
	out := map[string]any{"success": true, "message": fmt.Sprintf("node %q retired; it can be revived with action update or rollback", id)}
	if users := e.WF.NodeUsage(id); len(users) > 0 {
		out["used_by"] = users
		out["note"] = "these workflows still use the node and will fail at it until they are changed"
	}
	return out
}

func (e *Engine) rollbackNode(ctx context.Context, id string, version int) map[string]any {
	id = strings.ToLower(strings.TrimSpace(id))
	cur, ok := e.WF.GetNode(id)
	if !ok {
		return nodeFail("node %q does not exist", id)
	}
	old, err := e.WF.GetNodeVersion(id, version)
	if err != nil {
		versions, _ := e.WF.NodeVersions(id)
		return nodeFail("%v (existing versions: %v)", err, versions)
	}
	if old.Version == cur.Version && !cur.Retired {
		return nodeFail("version %d is already the current version", version)
	}
	if err := e.checkBackend(old.NodeSpec, true); err != nil {
		return nodeFail("%v", err)
	}
	out := e.commitNode(ctx, old.NodeSpec, cur, true)
	if ok, _ := out["success"].(bool); ok {
		out["message"] = fmt.Sprintf("version %d republished as version %d", old.Version, cur.Version+1)
	}
	return out
}

// ListNodes implements list_workflow_nodes.
func (e *Engine) ListNodes(in listNodesInput) map[string]any {
	if e.WF == nil {
		return nodeFail("workflow store unavailable")
	}
	if id := strings.ToLower(strings.TrimSpace(in.ID)); id != "" {
		m, ok := e.WF.GetNode(id)
		if !ok {
			return nodeFail("node %q does not exist", id)
		}
		versions, _ := e.WF.NodeVersions(id)
		out := map[string]any{"success": true, "node": m, "versions": versions}
		if users := e.WF.NodeUsage(id); len(users) > 0 {
			out["used_by"] = users
		}
		return out
	}
	all, err := e.WF.ListNodes(in.IncludeRetired)
	if err != nil {
		return nodeFail("%v", err)
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	items := make([]map[string]any, 0, len(all))
	for _, m := range all {
		hay := strings.ToLower(strings.Join([]string{m.ID, m.Name, m.Description, m.Category}, " "))
		if q == "" || strings.Contains(hay, q) {
			items = append(items, m.Summary())
		}
	}
	return map[string]any{"success": true, "count": len(items), "nodes": items}
}
