package workflow

import (
	"strings"
	"testing"
)

func toolSpec() NodeSpec {
	return NodeSpec{
		ID: "word_count", Description: "counts words",
		Inputs:  []NodeField{{Name: "text"}},
		Outputs: []NodeField{{Name: "words", Type: "integer"}},
		Backend: NodeBackend{Kind: "tool", Ref: "count_words"},
	}
}

func TestNormalizeFillsDefaults(t *testing.T) {
	s := toolSpec()
	s.ID = "  Word_Count "
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	if s.ID != "word_count" || s.Name != "word_count" {
		t.Fatalf("id/name = %q/%q", s.ID, s.Name)
	}
	if s.Inputs[0].Type != FieldString {
		t.Fatalf("input type defaults to string, got %q", s.Inputs[0].Type)
	}
	if s.Limits.TimeoutMS != 60000 || s.Limits.MaxOutputBytes != 256<<10 {
		t.Fatalf("limits = %+v", s.Limits)
	}
}

func TestNormalizeReportsEveryProblemAtOnce(t *testing.T) {
	s := NodeSpec{
		ID:      "X",
		Inputs:  []NodeField{{Name: "a"}, {Name: "a"}, {Name: "b", Type: "date"}, {Name: "1bad"}},
		Outputs: []NodeField{{Name: "o", Default: "x"}},
		Backend: NodeBackend{Kind: "tool", Ref: "t", Args: map[string]any{"q": "{{in.missing}}"}, Code: "print(1)"},
		Limits:  NodeLimits{TimeoutMS: 5},
	}
	err := s.Normalize()
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"id \"x\"", "description is required", "declared twice", "type \"date\"", "name must be", "outputs take no default", "{{in.missing}}", "tool takes ref and args only", "timeout_ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q:\n%v", want, err)
		}
	}
}

func TestNormalizePythonBackend(t *testing.T) {
	s := NodeSpec{ID: "make_report", Description: "d", Backend: NodeBackend{Kind: "python", Code: "print('{}')", EnvVars: []string{"API_KEY"}}}
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	if s.Backend.Ref != "node_make_report" {
		t.Fatalf("python backend runs as the generated tool node_<id>, got %q", s.Backend.Ref)
	}
	bad := NodeSpec{ID: "make_report", Description: "d", Backend: NodeBackend{Kind: "python", Code: " ", Args: map[string]any{"a": 1}, EnvVars: []string{"bad name"}}}
	err := bad.Normalize()
	if err == nil || !strings.Contains(err.Error(), "backend.code is required") || !strings.Contains(err.Error(), "python takes code and env_vars only") || !strings.Contains(err.Error(), "invalid environment variable name") {
		t.Fatalf("got %v", err)
	}
}

func TestNormalizeChecksDefaultsAndTests(t *testing.T) {
	s := toolSpec()
	s.Inputs = []NodeField{{Name: "n", Type: "integer", Default: "7"}, {Name: "mode", Enum: []string{"a", "b"}}}
	s.Tests = []NodeTest{{Inputs: map[string]any{"nope": 1}}, {Name: "ok", Expect: NodeExpect{Outputs: map[string]any{"ghost": 1}}}}
	err := s.Normalize()
	if err == nil || !strings.Contains(err.Error(), `input "nope" is not declared`) || !strings.Contains(err.Error(), `"ghost" which is not a declared output`) {
		t.Fatalf("got %v", err)
	}
	if s.Inputs[0].Default != int64(7) {
		t.Fatalf("default is coerced to its type, got %#v", s.Inputs[0].Default)
	}
	if s.Tests[0].Name != "test_1" {
		t.Fatalf("test name default = %q", s.Tests[0].Name)
	}
}

func TestCoerceValue(t *testing.T) {
	cases := []struct {
		typ  string
		in   any
		want any
		bad  bool
	}{
		{"string", 3.0, "3", false},
		{"string", map[string]any{}, nil, true},
		{"number", " 2.5 ", 2.5, false},
		{"number", "x", nil, true},
		{"integer", "42", int64(42), false},
		{"integer", 3.0, int64(3), false},
		{"integer", 3.5, nil, true},
		{"integer", "7.0", int64(7), false},
		{"boolean", "Yes", true, false},
		{"boolean", "maybe", nil, true},
		{"boolean", true, true, false},
	}
	for _, c := range cases {
		got, err := coerceValue(c.typ, c.in)
		if c.bad {
			if err == nil {
				t.Errorf("coerce(%s, %#v) should fail, got %#v", c.typ, c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("coerce(%s, %#v) = %#v, %v; want %#v", c.typ, c.in, got, err, c.want)
		}
	}
	v, err := coerceValue(FieldJSON, `{"a":[1,2]}`)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := v.(map[string]any); !ok || len(m) != 1 {
		t.Fatalf("json text decodes, got %#v", v)
	}
	if _, err := coerceValue(FieldJSON, "not json"); err == nil {
		t.Fatal("non-JSON text must be rejected for a json field")
	}
}

func TestCustomNodeID(t *testing.T) {
	cases := []struct {
		n    Node
		want string
	}{
		{Node{Type: "custom:Word_Count"}, "word_count"},
		{Node{Type: "custom", Config: map[string]any{"node_id": " Word_Count "}}, "word_count"},
		{Node{Type: "custom_node", ToolName: "word_count"}, "word_count"},
		{Node{Type: "tool", ToolName: "word_count"}, ""},
		{Node{Type: "custom"}, ""},
	}
	for _, c := range cases {
		if got := CustomNodeID(c.n); got != c.want {
			t.Errorf("CustomNodeID(%+v) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestSummaryDescribesTheContract(t *testing.T) {
	s := toolSpec()
	s.Inputs = []NodeField{{Name: "text", Description: "what to count"}, {Name: "lang", Optional: true}}
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	sum := NodeManifest{NodeSpec: s, Version: 3}.Summary()
	if sum["use_as"] != "type custom:word_count" || sum["version"] != 3 || sum["backend"] != "tool" {
		t.Fatalf("summary = %v", sum)
	}
	ins := sum["inputs"].([]map[string]any)
	if ins[0]["required"] != true || ins[1]["required"] != nil {
		t.Fatalf("required flags = %v", ins)
	}
}
