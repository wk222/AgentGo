package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHarnessTracksSandboxParallel exercises harness mechanisms without a live
// model. Each subtest uses its own t.TempDir() (no repo workspace).
func TestHarnessTracksSandboxParallel(t *testing.T) {
	tracks := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"verify_loop_edit_test_fix", testTrackVerifyLoop},
		{"verify_disk_contains", testTrackVerifyDisk},
		{"eval_set_parallel_runner", testTrackParallelEvalSet},
		{"workspace_escape_blocked", testTrackWorkspaceEscape},
	}
	for _, tr := range tracks {
		tr := tr
		t.Run(tr.name, func(t *testing.T) {
			t.Parallel()
			tr.fn(t)
		})
	}
}

func testTrackVerifyLoop(t *testing.T) {
	root := t.TempDir()
	scratchRel := "scratch"
	scratch := filepath.Join(root, scratchRel)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	sumGo := "package scratch\n\nfunc Add(a, b int) int { return a + b + 1 }\n"
	sumTest := "package scratch\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n if Add(1,2) != 3 { t.Fatal() }\n}\n"
	kase := EvalCase{
		ID: "verify-broken", root: root, Scratch: scratchRel,
		Setup: []EvalFile{
			{Path: "scratch/sum.go", Content: sumGo},
			{Path: "scratch/sum_test.go", Content: sumTest},
			{Path: "scratch/go.mod", Content: "module scratch\ngo 1.22\n"},
		},
		VerifyCmd: &EvalCommand{Name: "go", Args: []string{"test", "./..."}, Dir: scratchRel, Timeout: 120},
	}
	cleanup, err := kase.prepareWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := kase.verify(context.Background()); err == nil {
		t.Fatal("go test should fail while Add is wrong")
	}
	fixed := filepath.Join(scratch, "sum.go")
	if err := os.WriteFile(fixed, []byte("package scratch\n\nfunc Add(a, b int) int { return a + b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := kase.verify(context.Background()); err != nil {
		t.Fatalf("go test should pass after fix: %v", err)
	}
}

func testTrackVerifyDisk(t *testing.T) {
	root := t.TempDir()
	kase := EvalCase{
		ID: "write-check", root: root, Scratch: "scratch",
		Setup:  []EvalFile{{Path: "scratch/out.txt", Content: "before"}},
		Verify: []EvalCheck{{Path: "scratch/out.txt", Contains: "before", NotContains: "SECRET"}},
	}
	cleanup, err := kase.prepareWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := kase.verify(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func testTrackParallelEvalSet(t *testing.T) {
	agent := stubAgent{out: "ok keyword"}
	set := EvalSet{
		ID: "parallel-smoke",
		Cases: []EvalCase{
			{ID: "a", Input: "q1", Keywords: []string{"ok"}},
			{ID: "b", Input: "q2", Keywords: []string{"ok"}},
			{ID: "c", Input: "q3", Keywords: []string{"ok"}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := EvaluateSet(ctx, agent, set, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OverallPassed || len(res.Cases) != 3 {
		t.Fatalf("parallel eval: %+v", res)
	}
}

func testTrackWorkspaceEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := inside(root, "../etc/passwd"); err == nil {
		t.Fatal("path escape must be rejected")
	}
}

type stubAgent struct{ out string }

func (s stubAgent) Generate(ctx context.Context, sessionID, input string) (string, error) {
	return s.out, nil
}
