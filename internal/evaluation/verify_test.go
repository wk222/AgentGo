package evaluation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// approvingAgent pauses for approval on each scripted step; after Resolve it
// performs the action (a file write) like the real run would, and a denial
// leaves the disk untouched.
type approvingAgent struct {
	root     string
	writes   []string // files written when the matching approval is granted
	resolved []string
	sawApprv []bool
	step     int
}

func (a *approvingAgent) Generate(ctx context.Context, sid, in string) (string, error) {
	out, _, err := a.GenerateMeasured(ctx, sid, in)
	return out, err
}

func (a *approvingAgent) GenerateMeasured(context.Context, string, string) (string, Metrics, error) {
	return "", Metrics{ModelCalls: 1}, &ApprovalPause{ID: "ap-0", Tool: "write_file"}
}

func (a *approvingAgent) ResolveApproval(_ context.Context, id string, approve bool) (string, Metrics, error) {
	a.resolved = append(a.resolved, id)
	a.sawApprv = append(a.sawApprv, approve)
	if !approve {
		return "", Metrics{}, nil
	}
	_ = os.WriteFile(filepath.Join(a.root, a.writes[a.step]), []byte("hello-eval"), 0o644)
	a.step++
	if a.step < len(a.writes) {
		return "", Metrics{ToolCalls: 1}, &ApprovalPause{ID: fmt.Sprintf("ap-%d", a.step), Tool: "write_file"}
	}
	return "done", Metrics{ToolCalls: 1}, nil
}

func TestApproveLoopContinuesThroughChainedApprovalsAndVerifiesDisk(t *testing.T) {
	root := t.TempDir()
	a := &approvingAgent{root: root, writes: []string{"a.txt", "b.txt"}}
	c := EvalCase{ID: "w", Input: "write two files", Decision: "approve", root: root,
		Verify: []EvalCheck{{Path: "a.txt", Contains: "hello-eval"}, {Path: "b.txt", Contains: "hello-eval"}}}
	r := runCase(context.Background(), a, "s", c)
	if !r.Passed {
		t.Fatalf("expected pass, got %q", r.ErrorMsg)
	}
	if len(a.resolved) != 2 || a.resolved[0] != "ap-0" || a.resolved[1] != "ap-1" {
		t.Fatalf("approvals settled in the wrong order: %v", a.resolved)
	}
	if r.Metrics.ToolCalls != 2 {
		t.Errorf("tool calls of the continued runs were not summed: %d", r.Metrics.ToolCalls)
	}
}

func TestDenyLeavesTheDiskUntouched(t *testing.T) {
	root := t.TempDir()
	a := &approvingAgent{root: root, writes: []string{"a.txt"}}
	c := EvalCase{ID: "d", Input: "write", Decision: "deny", root: root, Verify: []EvalCheck{{Path: "a.txt", Absent: true}}}
	if r := runCase(context.Background(), a, "s", c); !r.Passed {
		t.Fatalf("deny case failed: %q", r.ErrorMsg)
	}
	if len(a.sawApprv) != 1 || a.sawApprv[0] {
		t.Fatalf("expected one denial, got %v", a.sawApprv)
	}
	// The same case fails when the file shows up anyway.
	_ = os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o644)
	b := &approvingAgent{root: root, writes: []string{"a.txt"}}
	if r := runCase(context.Background(), b, "s", c); r.Passed || !strings.Contains(r.ErrorMsg, "should not") {
		t.Fatalf("a file written despite denial must fail the case, got passed=%v %q", r.Passed, r.ErrorMsg)
	}
}

func TestVerifyFailsWhenTheReplyClaimsSuccessButDiskDisagrees(t *testing.T) {
	root := t.TempDir()
	a := &scriptedAgent{script: []scriptedTurn{{out: "I wrote the file."}}}
	c := EvalCase{ID: "liar", Input: "write", ExpectedText: "wrote", root: root, Verify: []EvalCheck{{Path: "note.txt", Contains: "x"}}}
	if r := runCase(context.Background(), a, "s", c); r.Passed || !strings.Contains(r.ErrorMsg, "note.txt") {
		t.Fatalf("expected a disk verification failure, got passed=%v %q", r.Passed, r.ErrorMsg)
	}
}

func TestApprovalNeedsAResolver(t *testing.T) {
	paused := &ApprovalPause{ID: "x", Tool: "t"}
	a := &scriptedAgent{script: []scriptedTurn{{err: paused}}}
	r := runCase(context.Background(), a, "s", EvalCase{ID: "n", Input: "q", Decision: "approve"})
	if r.Passed || !strings.Contains(r.ErrorMsg, "ApprovalResolver") {
		t.Fatalf("expected a clear error, got passed=%v %q", r.Passed, r.ErrorMsg)
	}
}

func TestScratchIsResetFilledAndRemoved(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, ".scratch", "stale.txt")
	_ = os.MkdirAll(filepath.Dir(stale), 0o755)
	_ = os.WriteFile(stale, []byte("old"), 0o644)

	var seen string
	probe := &probeAgent{fn: func() {
		raw, _ := os.ReadFile(filepath.Join(root, ".scratch", "fixture.txt"))
		seen = string(raw)
		if _, err := os.Stat(stale); err == nil {
			seen += "+STALE"
		}
	}}
	c := EvalCase{ID: "s", Input: "q", ExpectedText: "ok", root: root, Scratch: ".scratch",
		Setup: []EvalFile{{Path: ".scratch/fixture.txt", Content: "fx"}}}
	if r := runCase(context.Background(), probe, "s", c); !r.Passed {
		t.Fatalf("failed: %q", r.ErrorMsg)
	}
	if seen != "fx" {
		t.Fatalf("agent saw %q, want the fixture without stale leftovers", seen)
	}
	if _, err := os.Stat(filepath.Join(root, ".scratch")); err == nil {
		t.Fatal("scratch directory was not removed after the run")
	}
}

type probeAgent struct{ fn func() }

func (p *probeAgent) Generate(context.Context, string, string) (string, error) {
	p.fn()
	return "ok", nil
}

func TestWorkspacePathsCannotEscape(t *testing.T) {
	root := t.TempDir()
	bad := []EvalCase{
		{ID: "dotdot", Input: "q", root: root, Scratch: "../out", Setup: []EvalFile{{Path: "../out/x", Content: "x"}}},
		{ID: "root", Input: "q", root: root, Scratch: ".", Setup: []EvalFile{{Path: "x", Content: "x"}}},
		{ID: "outside", Input: "q", root: root, Scratch: "s", Setup: []EvalFile{{Path: "other/x", Content: "x"}}},
		{ID: "verify", Input: "q", ExpectedText: "ok", root: root, Verify: []EvalCheck{{Path: "../etc/passwd", Contains: "x"}}},
	}
	for _, c := range bad {
		r := runCase(context.Background(), &probeAgent{fn: func() {}}, "s", c)
		if r.Passed {
			t.Errorf("%s: path escape was accepted", c.ID)
		}
	}
}

func TestWorkspaceCasesNeedTheWorkspaceFlag(t *testing.T) {
	c := EvalCase{ID: "w", Input: "q", ExpectedText: "ok", Verify: []EvalCheck{{Path: "x", Contains: "x"}}}
	r := runCase(context.Background(), &probeAgent{fn: func() {}}, "s", c)
	if r.Passed || !strings.Contains(r.ErrorMsg, "--workspace") {
		t.Fatalf("expected a --workspace hint, got passed=%v %q", r.Passed, r.ErrorMsg)
	}
}

func TestVerifyCmdExitCodeDecides(t *testing.T) {
	root := t.TempDir()
	goCmd := func(args ...string) *EvalCommand { return &EvalCommand{Name: "go", Args: args, Timeout: 60} }
	pass := EvalCase{ID: "p", root: root, VerifyCmd: goCmd("version")}
	if err := pass.verify(context.Background()); err != nil {
		t.Fatalf("`go version` should pass: %v", err)
	}
	fail := EvalCase{ID: "f", root: root, VerifyCmd: goCmd("definitely-not-a-subcommand")}
	if err := fail.verify(context.Background()); err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("a failing command must fail verification, got %v", err)
	}
}

func TestWorkspaceSuiteLoadsAndValidates(t *testing.T) {
	set, err := LoadSuite("suite_workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Cases) < 3 {
		t.Fatalf("expected the workspace cases, got %d", len(set.Cases))
	}
}

func TestValidateRejectsAMisspeltDecision(t *testing.T) {
	bad := EvalSet{Cases: []EvalCase{{ID: "a", Decision: "aprove"}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("a misspelt decision would silently deny; it must be rejected")
	}
	for _, s := range []EvalSet{
		{Cases: []EvalCase{{ID: "a", Decision: "approve", ExpectApproval: true}}},
		{Cases: []EvalCase{{ID: "a", Setup: []EvalFile{{Path: "x"}}}}},
		{Cases: []EvalCase{{ID: "a"}, {ID: "a"}}},
	} {
		if s.Validate() == nil {
			t.Errorf("invalid suite accepted: %+v", s)
		}
	}
}