package workflow

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// CustomNodePrefix marks a node type that runs a custom node: "custom:<id>".
// The generic form is type "custom" with config.node_id.
const CustomNodePrefix = "custom:"

// Backends a custom node can run on. Every one reuses a capability that is
// already governed: a registered tool (built-in, MCP, generated, external
// agent, inner app, workflow_*), another workflow, or generated Python.
const (
	BackendTool        = "tool"
	BackendSubworkflow = "subworkflow"
	BackendPython      = "python"
)

const (
	defaultNodeTimeoutMS = 60_000
	maxNodeTimeoutMS     = 600_000
	defaultNodeOutput    = 256 << 10
	maxNodeOutput        = 4 << 20
)

var (
	nodeIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9_]{2,47}$`)
	nodeFieldPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,39}$`)
	nodeToolRef      = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)
	nodeEnvName      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// NodeField declares one input or output of a custom node.
type NodeField struct {
	Name        string   `json:"name" jsonschema:"description=identifier: letters and digits and underscore; starts with a letter"`
	Type        string   `json:"type,omitempty" jsonschema:"description=string (default) or number or integer or boolean or json"`
	Description string   `json:"description,omitempty"`
	Optional    bool     `json:"optional,omitempty" jsonschema:"description=input: may be absent. output: the backend may omit it"`
	Default     any      `json:"default,omitempty" jsonschema:"description=input only: used when nothing supplies the input; makes it optional"`
	Enum        []string `json:"enum,omitempty" jsonschema:"description=string fields only: the allowed values"`
}

// Required reports whether a run must supply this input (or the backend produce this output).
func (f NodeField) Required() bool { return !f.Optional && f.Default == nil }

// NodeBackend says what a custom node runs. Fill the fields of its kind only.
type NodeBackend struct {
	Kind          string         `json:"kind" jsonschema:"description=tool or subworkflow or python"`
	Ref           string         `json:"ref,omitempty" jsonschema:"description=tool: the registered tool name (built-in or MCP or generated or workflow_NAME or external_agent or invoke_inner_app). subworkflow: the workflow id. python: filled in for you"`
	Args          map[string]any `json:"args,omitempty" jsonschema:"description=tool: the argument object for the tool. String values may use {{in.NAME}} for an input plus {{input}} {{last}} {{var.X}}. Omit it to pass the inputs object as-is"`
	InputTemplate string         `json:"input_template,omitempty" jsonschema:"description=subworkflow: the text handed to the workflow; may use {{in.NAME}}. Omit it: the single input's value or else the inputs as JSON"`
	Code          string         `json:"code,omitempty" jsonschema:"description=python: script body. The dict args holds the inputs. Print ONE JSON object whose keys are the declared outputs as the last stdout line"`
	EnvVars       []string       `json:"env_vars,omitempty" jsonschema:"description=python: names of host environment variables the code may read (secrets are withheld otherwise)"`
}

// NodeLimits bounds one run of a custom node.
type NodeLimits struct {
	TimeoutMS      int `json:"timeout_ms,omitempty" jsonschema:"description=default 60000; at most 600000"`
	MaxOutputBytes int `json:"max_output_bytes,omitempty" jsonschema:"description=default 262144; at most 4194304; a larger backend result fails the node"`
}

// NodeExpect is what a contract test checks beyond "ran and matches the declared outputs".
type NodeExpect struct {
	Contains      string         `json:"contains,omitempty" jsonschema:"description=text the node output must contain (case-insensitive)"`
	Outputs       map[string]any `json:"outputs,omitempty" jsonschema:"description=declared output name to the exact value it must have"`
	ErrorContains string         `json:"error_contains,omitempty" jsonschema:"description=negative test: the node must fail with an error containing this text"`
}

// NodeTest is one contract test. Tests really run the backend.
type NodeTest struct {
	Name   string         `json:"name,omitempty"`
	Inputs map[string]any `json:"inputs,omitempty" jsonschema:"description=input name to value"`
	Expect NodeExpect     `json:"expect,omitempty"`
}

// NodeSpec is what an agent authors: a reusable workflow node with a typed
// input/output contract. Whatever runs behind it is a black box to the workflow.
type NodeSpec struct {
	ID          string      `json:"id" jsonschema:"description=snake_case id (3-48 chars; a-z 0-9 _); workflows use it as type custom:ID"`
	Name        string      `json:"name,omitempty" jsonschema:"description=display name"`
	Description string      `json:"description" jsonschema:"description=what the node does and when to use it; other agents read this to pick nodes"`
	Category    string      `json:"category,omitempty"`
	Inputs      []NodeField `json:"inputs,omitempty"`
	Outputs     []NodeField `json:"outputs,omitempty" jsonschema:"description=declared outputs. none declared: the node passes the backend text through unchanged"`
	Backend     NodeBackend `json:"backend"`
	SideEffects string      `json:"side_effects,omitempty" jsonschema:"description=none or read or write or external (informational)"`
	Idempotent  bool        `json:"idempotent,omitempty" jsonschema:"description=true when running it twice with the same inputs is harmless (informational)"`
	Limits      NodeLimits  `json:"limits,omitempty"`
	Tests       []NodeTest  `json:"tests,omitempty" jsonschema:"description=contract tests; at least one is required to publish"`
}

// NodeManifest is a stored NodeSpec plus what the store manages.
type NodeManifest struct {
	NodeSpec
	Version   int   `json:"version"`
	Retired   bool  `json:"retired,omitempty"`
	CreatedAt int64 `json:"created_at,omitempty"`
	UpdatedAt int64 `json:"updated_at,omitempty"`
}

// Timeout is the effective per-run timeout (records written before limits existed carry zeros).
func (m NodeManifest) Timeout() time.Duration {
	ms := m.Limits.TimeoutMS
	if ms <= 0 {
		ms = defaultNodeTimeoutMS
	}
	return time.Duration(ms) * time.Millisecond
}

// OutputLimit is the effective cap on a backend result.
func (m NodeManifest) OutputLimit() int {
	if m.Limits.MaxOutputBytes <= 0 {
		return defaultNodeOutput
	}
	return m.Limits.MaxOutputBytes
}

// IsCustomNodeType reports whether a node type runs a custom node.
func IsCustomNodeType(typ string) bool {
	t := strings.ToLower(strings.TrimSpace(typ))
	return t == "custom" || t == "custom_node" || strings.HasPrefix(t, CustomNodePrefix)
}

// CustomNodeID returns the custom node a workflow node runs ("" when it is not one):
// the id after "custom:", else config.node_id, else tool_name.
func CustomNodeID(n Node) string {
	if !IsCustomNodeType(n.Type) {
		return ""
	}
	t := strings.ToLower(strings.TrimSpace(n.Type))
	if strings.HasPrefix(t, CustomNodePrefix) {
		return strings.TrimSpace(t[len(CustomNodePrefix):])
	}
	if n.Config != nil {
		if s, ok := n.Config["node_id"].(string); ok && strings.TrimSpace(s) != "" {
			return strings.ToLower(strings.TrimSpace(s))
		}
	}
	return strings.ToLower(strings.TrimSpace(n.ToolName))
}

// Normalize fills defaults and checks everything that can be known without
// running the node. All problems are reported together so an agent can fix
// them in one pass.
func (s *NodeSpec) Normalize() error {
	var bad []string
	add := func(format string, a ...any) { bad = append(bad, fmt.Sprintf(format, a...)) }

	s.ID = strings.ToLower(strings.TrimSpace(s.ID))
	if !nodeIDPattern.MatchString(s.ID) {
		add("id %q must be snake_case: a-z 0-9 _, starting with a letter, 3-48 chars", s.ID)
	}
	if s.Name = strings.TrimSpace(s.Name); s.Name == "" {
		s.Name = s.ID
	}
	if s.Description = strings.TrimSpace(s.Description); s.Description == "" {
		add("description is required (other agents read it to decide whether to use the node)")
	}
	s.Category = strings.TrimSpace(s.Category)

	s.Inputs = normalizeFields("input", s.Inputs, add)
	s.Outputs = normalizeFields("output", s.Outputs, add)
	declared := map[string]bool{}
	for _, f := range s.Inputs {
		declared[f.Name] = true
	}

	s.normalizeBackend(declared, add)

	s.SideEffects = strings.ToLower(strings.TrimSpace(s.SideEffects))
	switch s.SideEffects {
	case "", "none", "read", "write", "external":
	default:
		add("side_effects %q must be none, read, write or external", s.SideEffects)
	}

	switch t := s.Limits.TimeoutMS; {
	case t == 0:
		s.Limits.TimeoutMS = defaultNodeTimeoutMS
	case t < 100 || t > maxNodeTimeoutMS:
		add("limits.timeout_ms must be 100-%d, got %d", maxNodeTimeoutMS, t)
	}
	switch o := s.Limits.MaxOutputBytes; {
	case o == 0:
		s.Limits.MaxOutputBytes = defaultNodeOutput
	case o < 1024 || o > maxNodeOutput:
		add("limits.max_output_bytes must be 1024-%d, got %d", maxNodeOutput, o)
	}

	s.normalizeTests(declared, add)

	if len(bad) > 0 {
		return fmt.Errorf("invalid node: %s", strings.Join(bad, "; "))
	}
	return nil
}

func normalizeFields(kind string, in []NodeField, add func(string, ...any)) []NodeField {
	out := make([]NodeField, 0, len(in))
	seen := map[string]bool{}
	for i, f := range in {
		f.Name = strings.TrimSpace(f.Name)
		f.Type = strings.ToLower(strings.TrimSpace(f.Type))
		if f.Type == "" {
			f.Type = FieldString
		}
		label := fmt.Sprintf("%s #%d", kind, i+1)
		if f.Name != "" {
			label = fmt.Sprintf("%s %q", kind, f.Name)
		}
		switch {
		case !nodeFieldPattern.MatchString(f.Name):
			add("%s: name must be letters, digits and _, starting with a letter (max 40)", label)
		case seen[f.Name]:
			add("%s is declared twice", label)
		}
		seen[f.Name] = true
		if !validFieldType(f.Type) {
			add("%s: type %q must be string, number, integer, boolean or json", label, f.Type)
			out = append(out, f)
			continue
		}
		if len(f.Enum) > 0 && f.Type != FieldString {
			add("%s: enum is only for string fields", label)
		}
		if kind == "output" && (f.Default != nil || len(f.Enum) > 0) {
			add("%s: outputs take no default or enum", label)
		}
		if f.Default != nil {
			v, err := coerceValue(f.Type, f.Default)
			if err != nil {
				add("%s: default: %v", label, err)
			} else {
				f.Default = v
			}
		}
		out = append(out, f)
	}
	return out
}

func (s *NodeSpec) normalizeBackend(declared map[string]bool, add func(string, ...any)) {
	b := &s.Backend
	b.Kind = strings.ToLower(strings.TrimSpace(b.Kind))
	b.Ref = strings.TrimSpace(b.Ref)
	switch b.Kind {
	case BackendTool:
		if !nodeToolRef.MatchString(b.Ref) {
			add("backend.ref must name a registered tool (letters digits _ . - up to 64), got %q", b.Ref)
		}
		if b.Code != "" || len(b.EnvVars) > 0 || b.InputTemplate != "" {
			add("backend kind tool takes ref and args only")
		}
	case BackendSubworkflow:
		if b.Ref == "" {
			add("backend.ref must be the id of the workflow to run")
		}
		if b.Code != "" || len(b.EnvVars) > 0 || len(b.Args) > 0 {
			add("backend kind subworkflow takes ref and input_template only")
		}
	case BackendPython:
		if strings.TrimSpace(b.Code) == "" {
			add("backend.code is required for kind python")
		}
		if len(b.Args) > 0 || b.InputTemplate != "" {
			add("backend kind python takes code and env_vars only")
		}
		for _, n := range b.EnvVars {
			if !nodeEnvName.MatchString(n) {
				add("backend.env_vars: invalid environment variable name %q", n)
			}
		}
		b.Ref = "node_" + s.ID
	default:
		add("backend.kind %q must be tool, subworkflow or python", b.Kind)
	}
	check := func(text string) {
		for _, m := range inRefPattern.FindAllStringSubmatch(text, -1) {
			if !declared[m[1]] {
				add("backend refers to {{in.%s}} but no input %q is declared", m[1], m[1])
			}
		}
	}
	walkStrings(b.Args, check)
	check(b.InputTemplate)
}

func (s *NodeSpec) normalizeTests(declaredIn map[string]bool, add func(string, ...any)) {
	outs := map[string]bool{}
	for _, f := range s.Outputs {
		outs[f.Name] = true
	}
	for i := range s.Tests {
		t := &s.Tests[i]
		if t.Name = strings.TrimSpace(t.Name); t.Name == "" {
			t.Name = fmt.Sprintf("test_%d", i+1)
		}
		for k := range t.Inputs {
			if !declaredIn[k] {
				add("test %q: input %q is not declared", t.Name, k)
			}
		}
		for k := range t.Expect.Outputs {
			ok := outs[k]
			if len(outs) == 0 {
				ok = k == "result"
			}
			if !ok {
				add("test %q: expect.outputs names %q which is not a declared output", t.Name, k)
			}
		}
	}
}

// Summary is the compact, agent-readable form used by catalogs and listings.
func (m NodeManifest) Summary() map[string]any {
	fields := func(fs []NodeField, input bool) []map[string]any {
		out := make([]map[string]any, 0, len(fs))
		for _, f := range fs {
			e := map[string]any{"name": f.Name, "type": f.Type}
			if f.Description != "" {
				e["description"] = f.Description
			}
			if f.Required() {
				e["required"] = true
			}
			if input && f.Default != nil {
				e["default"] = f.Default
			}
			if len(f.Enum) > 0 {
				e["enum"] = f.Enum
			}
			out = append(out, e)
		}
		return out
	}
	s := map[string]any{
		"id": m.ID, "name": m.Name, "description": m.Description, "version": m.Version,
		"use_as":  "type custom:" + m.ID,
		"inputs":  fields(m.Inputs, true),
		"outputs": fields(m.Outputs, false),
		"backend": m.Backend.Kind,
	}
	if m.Category != "" {
		s["category"] = m.Category
	}
	if m.SideEffects != "" {
		s["side_effects"] = m.SideEffects
	}
	if m.Idempotent {
		s["idempotent"] = true
	}
	if m.Retired {
		s["retired"] = true
	}
	return s
}
