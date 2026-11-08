package workflow

import (
	"context"
	"strings"
	"testing"
)

func counterNode(t *testing.T, env *nodeTestEnv, tests ...NodeTest) NodeManifest {
	t.Helper()
	env.tools["count"] = func(_ context.Context, args string) (string, error) {
		if strings.Contains(args, "boom") {
			return "", context.DeadlineExceeded
		}
		return `{"words": 3, "preview": "a b c"}`, nil
	}
	return env.add(t, NodeSpec{
		ID: "word_counter", Description: "counts words",
		Inputs:  []NodeField{{Name: "text"}},
		Outputs: []NodeField{{Name: "words", Type: "integer"}, {Name: "preview", Optional: true}},
		Backend: NodeBackend{Kind: "tool", Ref: "count"},
		Tests:   tests,
	})
}

func TestContractTestsPassWhenTheNodeMeetsItsContract(t *testing.T) {
	env := newNodeEnv()
	m := counterNode(t, env,
		NodeTest{Name: "basic", Inputs: map[string]any{"text": "a b c"}, Expect: NodeExpect{Outputs: map[string]any{"words": 3}, Contains: "A B C"}},
		NodeTest{Name: "fails loudly", Inputs: map[string]any{"text": "boom"}, Expect: NodeExpect{ErrorContains: "deadline"}},
	)
	rs := RunContractTests(context.Background(), m, env.rc())
	if !AllPassed(rs) || len(rs) != 2 {
		t.Fatalf("results = %+v", rs)
	}
}

func TestContractTestsReportEachWayToFail(t *testing.T) {
	env := newNodeEnv()
	m := counterNode(t, env,
		NodeTest{Name: "wrong value", Inputs: map[string]any{"text": "x"}, Expect: NodeExpect{Outputs: map[string]any{"words": 4}}},
		NodeTest{Name: "wrong text", Inputs: map[string]any{"text": "x"}, Expect: NodeExpect{Contains: "zebra"}},
		NodeTest{Name: "no input", Inputs: nil},
		NodeTest{Name: "expected error but ok", Inputs: map[string]any{"text": "x"}, Expect: NodeExpect{ErrorContains: "nope"}},
		NodeTest{Name: "backend error", Inputs: map[string]any{"text": "boom"}},
		NodeTest{Name: "wrong error", Inputs: map[string]any{"text": "boom"}, Expect: NodeExpect{ErrorContains: "xyz"}},
	)
	rs := RunContractTests(context.Background(), m, env.rc())
	wants := []string{`output "words" = 3, expected 4`, `does not contain "zebra"`, `input "text" is required`, "but the node succeeded", "deadline", `expected an error containing "xyz"`}
	if len(rs) != len(wants) {
		t.Fatalf("results = %+v", rs)
	}
	for i, w := range wants {
		if rs[i].Passed || !strings.Contains(rs[i].Error, w) {
			t.Errorf("%s: passed=%v error=%q, want it to contain %q", rs[i].Name, rs[i].Passed, rs[i].Error, w)
		}
	}
	if AllPassed(rs) {
		t.Fatal("AllPassed must be false")
	}
}

func TestContractTestsCheckTheDeclaredOutputsEvenWithoutExpectations(t *testing.T) {
	env := newNodeEnv()
	m := counterNode(t, env, NodeTest{Name: "smoke", Inputs: map[string]any{"text": "x"}})
	env.tools["count"] = func(context.Context, string) (string, error) { return `{"preview": "no words key"}`, nil }
	rs := RunContractTests(context.Background(), m, env.rc())
	if rs[0].Passed || !strings.Contains(rs[0].Error, `output "words" is missing`) {
		t.Fatalf("a result that breaks the output contract must fail even a bare smoke test: %+v", rs[0])
	}
}

func TestAllPassedNeedsAtLeastOneTest(t *testing.T) {
	if AllPassed(nil) {
		t.Fatal("no tests is not a pass")
	}
}

func TestEachContractTestGetsFreshVariables(t *testing.T) {
	env := newNodeEnv()
	m := counterNode(t, env,
		NodeTest{Name: "one", Inputs: map[string]any{"text": "a"}},
		NodeTest{Name: "two", Inputs: map[string]any{"text": "b"}},
	)
	rc := env.rc()
	rc.Vars = map[string]string{"keep": "me"}
	RunContractTests(context.Background(), m, rc)
	if len(rc.Vars) != 1 || rc.Vars["keep"] != "me" {
		t.Fatalf("tests must not write into the caller's variables: %v", rc.Vars)
	}
}
