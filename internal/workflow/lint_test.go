package workflow

import (
	"strings"
	"testing"
)

func lintDef(id string, nodes ...Node) Definition {
	all := []Node{{ID: "s", Type: "start"}}
	all = append(all, nodes...)
	all = append(all, Node{ID: "e", Type: "end"})
	var edges []Edge
	for i := 0; i+1 < len(all); i++ {
		edges = append(edges, Edge{From: all[i].ID, To: all[i+1].ID})
	}
	return Definition{ID: id, Name: id, Nodes: all, Edges: edges}
}

func hasIssue(issues []LintIssue, level, node, contains string) bool {
	for _, i := range issues {
		if i.Level == level && (node == "" || i.NodeID == node) && strings.Contains(i.Message, contains) {
			return true
		}
	}
	return false
}

func TestLintAcceptsAHealthyWorkflow(t *testing.T) {
	def := lintDef("ok", Node{ID: "t", Type: "tool", ToolName: "get_current_time", ArgsJSON: "{}"}, Node{ID: "l", Type: "llm", Prompt: "{{last}}"})
	issues := Lint(def, LintEnv{ToolExists: func(string) bool { return true }})
	if len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestLintFindsUnknownTypesAndBadToolNodes(t *testing.T) {
	def := lintDef("bad",
		Node{ID: "a", Type: "teleport"},
		Node{ID: "b", Type: "tool"},
		Node{ID: "c", Type: "tool", ToolName: "no_such_tool"},
		Node{ID: "d", Type: "tool", ToolName: "real", ArgsJSON: "{oops"},
	)
	issues := Lint(def, LintEnv{ToolExists: func(n string) bool { return n == "real" }})
	for _, w := range []struct{ node, msg string }{
		{"a", `unknown node type "teleport"`},
		{"b", "no tool_name"},
		{"c", `tool "no_such_tool" is not registered`},
		{"d", "args_json is not valid JSON"},
	} {
		if !hasIssue(issues, LintError, w.node, w.msg) {
			t.Errorf("missing %s: %s in %+v", w.node, w.msg, issues)
		}
	}
	if len(LintErrors(issues)) != len(issues) {
		t.Fatal("all of these are errors")
	}
}

func TestLintWarnsThatToolNodesDoNotExpandPlaceholders(t *testing.T) {
	def := lintDef("tpl", Node{ID: "t", Type: "tool", ToolName: "web_search", ArgsJSON: `{"query":"{{input}}"}`})
	issues := Lint(def, LintEnv{ToolExists: func(string) bool { return true }})
	if len(issues) != 1 || !hasIssue(issues, LintWarning, "t", "do not expand {{input}}") || len(LintErrors(issues)) != 0 {
		t.Fatalf("a valid-but-unexpanded placeholder is a warning, not an error: %+v", issues)
	}
}

func TestLintChecksSubworkflowTargets(t *testing.T) {
	def := lintDef("top", Node{ID: "a", Type: "subworkflow"}, Node{ID: "b", Type: "subworkflow", ToolName: "ghost"}, Node{ID: "c", Type: "workflow", Config: map[string]any{"workflow_id": "there"}})
	env := LintEnv{Workflow: func(id string) (Definition, bool) {
		if id == "there" {
			return lintDef("there"), true
		}
		return Definition{}, false
	}}
	issues := Lint(def, env)
	if !hasIssue(issues, LintError, "a", "needs the target workflow id") || !hasIssue(issues, LintError, "b", `workflow "ghost" does not exist`) {
		t.Fatalf("issues = %+v", issues)
	}
	if hasIssue(issues, LintError, "c", "") {
		t.Fatalf("an existing target is fine: %+v", issues)
	}
}

func TestLintFindsWorkflowCyclesStatically(t *testing.T) {
	a := lintDef("wf_a", Node{ID: "call_b", Type: "subworkflow", ToolName: "wf_b"})
	b := lintDef("wf_b", Node{ID: "call_a", Type: "subworkflow", ToolName: "wf_a"})
	env := LintEnv{Workflow: func(id string) (Definition, bool) {
		switch id {
		case "wf_a":
			return a, true
		case "wf_b":
			return b, true
		}
		return Definition{}, false
	}}
	issues := Lint(a, env)
	if !hasIssue(issues, LintError, "call_b", "workflow cycle: wf_a -> wf_b -> wf_a") {
		t.Fatalf("issues = %+v", issues)
	}
	self := lintDef("loner", Node{ID: "me", Type: "subworkflow", ToolName: "loner"})
	if issues := Lint(self, LintEnv{}); !hasIssue(issues, LintError, "me", "workflow cycle: loner -> loner") {
		t.Fatalf("self reference without any store: %+v", issues)
	}
	// The workflow being saved replaces the stored copy of itself: a stale stored
	// version that called back must not cause a false alarm.
	fixed := lintDef("wf_a", Node{ID: "call_b", Type: "subworkflow", ToolName: "wf_b"})
	b2 := lintDef("wf_b")
	env2 := LintEnv{Workflow: func(id string) (Definition, bool) {
		if id == "wf_b" {
			return b2, true
		}
		return a, true // stale: still points at wf_b which points at nothing now
	}}
	if issues := Lint(fixed, env2); len(issues) != 0 {
		t.Fatalf("issues = %+v", issues)
	}
}

func TestLintFindsTooDeepNesting(t *testing.T) {
	t.Setenv("AGENTGO_WORKFLOW_MAX_DEPTH", "2")
	w1 := lintDef("w1", Node{ID: "n", Type: "subworkflow", ToolName: "w2"})
	w2 := lintDef("w2", Node{ID: "n", Type: "subworkflow", ToolName: "w3"})
	w3 := lintDef("w3")
	env := LintEnv{Workflow: func(id string) (Definition, bool) {
		return map[string]Definition{"w1": w1, "w2": w2, "w3": w3}[id], id != ""
	}}
	if issues := Lint(w1, env); !hasIssue(issues, LintError, "", "nest 3 deep but the limit is 2") {
		t.Fatalf("issues = %+v", issues)
	}
}

func lintNodes() LintEnv {
	m := NodeManifest{NodeSpec: NodeSpec{
		ID: "geo_lookup", Description: "d",
		Inputs:  []NodeField{{Name: "city"}, {Name: "limit", Type: "integer"}},
		Backend: NodeBackend{Kind: "tool", Ref: "geo"},
	}}
	retired := NodeManifest{NodeSpec: NodeSpec{ID: "old_node"}, Retired: true}
	sys := NodeManifest{NodeSpec: NodeSpec{ID: "whole_system", Backend: NodeBackend{Kind: "subworkflow", Ref: "wf_system"}}}
	return LintEnv{Node: func(id string) (NodeManifest, bool) {
		switch id {
		case "geo_lookup":
			return m, true
		case "old_node":
			return retired, true
		case "whole_system":
			return sys, true
		}
		return NodeManifest{}, false
	}}
}

func TestLintChecksCustomNodeContracts(t *testing.T) {
	def := lintDef("uses_nodes",
		Node{ID: "ghost", Type: "custom:ghost_node"},
		Node{ID: "old", Type: "custom:old_node"},
		Node{ID: "noid", Type: "custom"},
		Node{ID: "typo", Type: "custom:geo_lookup", Config: map[string]any{"inputs": map[string]any{"cty": "x"}}},
		Node{ID: "badtype", Type: "custom:geo_lookup", Config: map[string]any{"inputs": map[string]any{"city": "Rome", "limit": "many"}}},
		Node{ID: "tpl", Type: "custom:geo_lookup", Config: map[string]any{"inputs": map[string]any{"city": "{{input}}", "limit": "{{var.n}}"}}},
		Node{ID: "partial", Type: "custom:geo_lookup", Config: map[string]any{"inputs": map[string]any{"city": "Rome"}}},
	)
	issues := Lint(def, lintNodes())
	for _, w := range []struct{ level, node, msg string }{
		{LintError, "ghost", `custom node "ghost_node" is not defined`},
		{LintError, "old", "is retired"},
		{LintError, "noid", "has no id"},
		{LintError, "typo", `no input "cty" (declared: city, limit)`},
		{LintError, "badtype", `input "limit"`},
		{LintWarning, "partial", `input "limit" has no value in config.inputs`},
	} {
		if !hasIssue(issues, w.level, w.node, w.msg) {
			t.Errorf("missing %s %s: %s\n%s", w.level, w.node, w.msg, FormatLint(issues))
		}
	}
	if hasIssue(issues, "", "tpl", "") {
		t.Errorf("template values are only known at run time and must not be flagged: %s", FormatLint(issues))
	}
}

func TestLintSeesCyclesThroughCustomNodesBackedByWorkflows(t *testing.T) {
	sysDef := lintDef("wf_system", Node{ID: "n", Type: "custom:whole_system"})
	env := lintNodes()
	env.Workflow = func(id string) (Definition, bool) { return sysDef, id == "wf_system" }
	if issues := Lint(sysDef, env); !hasIssue(issues, LintError, "n", "workflow cycle: wf_system -> wf_system") {
		t.Fatalf("a node that wraps a workflow back into itself must be caught: %+v", issues)
	}
}

func TestFormatLintListsErrorsFirst(t *testing.T) {
	got := FormatLint([]LintIssue{{Level: LintWarning, NodeID: "w", Message: "m1"}, {Level: LintError, NodeID: "e", Message: "m2"}})
	if got != "error [e]: m2\nwarning [w]: m1" {
		t.Fatalf("got %q", got)
	}
}
