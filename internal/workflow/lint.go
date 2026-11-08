package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Lint levels. An error means the workflow would fail when it runs; a warning is worth a look.
const (
	LintError   = "error"
	LintWarning = "warning"
)

// LintIssue is one finding about a workflow definition.
type LintIssue struct {
	Level   string `json:"level"`
	NodeID  string `json:"node,omitempty"`
	Message string `json:"message"`
}

// LintEnv tells Lint what exists. A nil hook skips the checks that need it.
type LintEnv struct {
	ToolExists func(name string) bool
	Workflow   func(id string) (Definition, bool)
	Node       func(id string) (NodeManifest, bool)
}

// LintErrors returns only the errors among issues.
func LintErrors(issues []LintIssue) []LintIssue {
	var out []LintIssue
	for _, i := range issues {
		if i.Level == LintError {
			out = append(out, i)
		}
	}
	return out
}

// FormatLint renders issues as one line each, errors first.
func FormatLint(issues []LintIssue) string {
	sorted := append([]LintIssue(nil), issues...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Level == LintError && sorted[j].Level != LintError })
	lines := make([]string, 0, len(sorted))
	for _, i := range sorted {
		where := ""
		if i.NodeID != "" {
			where = " [" + i.NodeID + "]"
		}
		lines = append(lines, i.Level+where+": "+i.Message)
	}
	return strings.Join(lines, "\n")
}

func subworkflowRef(n Node) string {
	if id := strings.TrimSpace(n.ToolName); id != "" {
		return id
	}
	if n.Config != nil {
		if s, ok := n.Config["workflow_id"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// Lint finds what ValidateWorkflow's structure check cannot: node types that do
// not exist, tools and workflows that are not there, custom nodes whose
// contract the node does not meet, and workflows that reach themselves.
func Lint(def Definition, env LintEnv) []LintIssue {
	var issues []LintIssue
	add := func(level, node, format string, a ...any) {
		issues = append(issues, LintIssue{Level: level, NodeID: node, Message: fmt.Sprintf(format, a...)})
	}
	registry := NewExecutorRegistry()
	rootKey := workflowKey(def)

	for _, n := range def.Nodes {
		typ := strings.ToLower(strings.TrimSpace(n.Type))
		if registry.Get(typ) == nil {
			add(LintError, n.ID, "unknown node type %q", n.Type)
			continue
		}
		switch {
		case typ == "tool":
			lintToolNode(n, env, add)
		case typ == "subworkflow" || typ == "workflow":
			id := subworkflowRef(n)
			switch {
			case id == "":
				add(LintError, n.ID, "subworkflow node needs the target workflow id in tool_name or config.workflow_id")
			case id != rootKey && env.Workflow != nil:
				if _, ok := env.Workflow(id); !ok {
					add(LintError, n.ID, "workflow %q does not exist (create it first)", id)
				}
			}
		case IsCustomNodeType(typ):
			lintCustomNode(n, env, add)
		}
	}
	lintNesting(def, env, add)
	return issues
}

func lintToolNode(n Node, env LintEnv, add func(level, node, format string, a ...any)) {
	name := strings.TrimSpace(n.ToolName)
	switch {
	case name == "":
		add(LintError, n.ID, "tool node has no tool_name")
	case env.ToolExists != nil && !env.ToolExists(name):
		add(LintError, n.ID, "tool %q is not registered (find the exact name with tool_search)", name)
	}
	args := strings.TrimSpace(n.ArgsJSON)
	if args != "" && !json.Valid([]byte(args)) {
		add(LintError, n.ID, "args_json is not valid JSON")
	}
	// A tool node hands args_json to the tool untouched, so a placeholder would arrive as literal text.
	if strings.Contains(args, "{{") {
		add(LintWarning, n.ID, "tool nodes do not expand {{input}} / {{last}} in args_json (the tool would get the literal text); leave args_json empty to pass the previous output as {\"input\": ...}, or use a custom node whose backend.args takes {{in.NAME}}")
	}
}

func lintCustomNode(n Node, env LintEnv, add func(level, node, format string, a ...any)) {
	id := CustomNodeID(n)
	if id == "" {
		add(LintError, n.ID, "custom node has no id: use type custom:<id> or config.node_id")
		return
	}
	if env.Node == nil {
		return
	}
	m, ok := env.Node(id)
	if !ok {
		add(LintError, n.ID, "custom node %q is not defined (list_workflow_nodes shows what exists)", id)
		return
	}
	if m.Retired {
		add(LintError, n.ID, "custom node %q is retired", id)
		return
	}
	supplied, err := suppliedInputs(n, nodeEnv{})
	if err != nil {
		add(LintError, n.ID, "%v", err)
		return
	}
	byName := map[string]NodeField{}
	for _, f := range m.Inputs {
		byName[f.Name] = f
	}
	for k, v := range supplied {
		f, known := byName[k]
		if !known {
			add(LintError, n.ID, "node %q has no input %q (declared: %s)", id, k, fieldNames(m.Inputs))
			continue
		}
		if s, isText := v.(string); isText && strings.Contains(s, "{{") {
			continue // a template: only known at run time
		}
		if _, err := coerceValue(f.Type, v); err != nil {
			add(LintError, n.ID, "input %q: %v", k, err)
		}
	}
	if len(m.Inputs) > 1 {
		for _, f := range m.Inputs {
			if _, given := supplied[f.Name]; !given && f.Required() {
				add(LintWarning, n.ID, "input %q has no value in config.inputs; at run time it is read from key %q of the previous node's JSON output", f.Name, f.Name)
			}
		}
	}
}

// lintNesting finds cycles and too-deep nesting through subworkflow nodes and
// custom nodes backed by a workflow, using root (not the stored copy) for itself.
func lintNesting(root Definition, env LintEnv, add func(level, node, format string, a ...any)) {
	rootKey := workflowKey(root)
	if rootKey == "" {
		return
	}
	type ref struct{ to, node string }
	refsOf := func(d Definition) []ref {
		var out []ref
		for _, n := range d.Nodes {
			t := strings.ToLower(strings.TrimSpace(n.Type))
			switch {
			case t == "subworkflow" || t == "workflow":
				if id := subworkflowRef(n); id != "" {
					out = append(out, ref{id, n.ID})
				}
			case IsCustomNodeType(t) && env.Node != nil:
				if m, ok := env.Node(CustomNodeID(n)); ok && m.Backend.Kind == BackendSubworkflow && m.Backend.Ref != "" {
					out = append(out, ref{m.Backend.Ref, n.ID})
				}
			}
		}
		return out
	}
	load := func(id string) (Definition, bool) {
		if id == rootKey {
			return root, true
		}
		if env.Workflow == nil {
			return Definition{}, false
		}
		return env.Workflow(id)
	}

	const (
		onPath = 1
		done   = 2
	)
	state := map[string]int{}
	depth := map[string]int{}
	var path []string
	reported := false
	var visit func(key string, d Definition, rootNode string) int
	visit = func(key string, d Definition, rootNode string) int {
		state[key] = onPath
		path = append(path, key)
		best := 1
		for _, r := range refsOf(d) {
			node := rootNode
			if key == rootKey {
				node = r.node
			}
			switch state[r.to] {
			case onPath:
				if !reported {
					reported = true
					start := 0
					for i, p := range path {
						if p == r.to {
							start = i
						}
					}
					add(LintError, node, "workflow cycle: %s -> %s (a run would never finish)", strings.Join(path[start:], " -> "), r.to)
				}
			case done:
				if v := 1 + depth[r.to]; v > best {
					best = v
				}
			default:
				if sub, ok := load(r.to); ok {
					if v := 1 + visit(r.to, sub, node); v > best {
						best = v
					}
				}
			}
		}
		path = path[:len(path)-1]
		state[key] = done
		depth[key] = best
		return best
	}
	if d := visit(rootKey, root, ""); d > MaxWorkflowDepth() {
		add(LintError, "", "workflows nest %d deep but the limit is %d (AGENTGO_WORKFLOW_MAX_DEPTH); flatten the chain", d, MaxWorkflowDepth())
	}
}
