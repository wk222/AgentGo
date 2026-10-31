package governance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/pathgrant"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
)

func pathCtx(root, mode, args string) ToolPolicyContext {
	return ToolPolicyContext{ToolName: "read_xlsx", Arguments: args, AllowedRoot: root, Control: ControlPolicyFromMode(mode)}
}

func jsonPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }

func TestPathPolicyAsksBeforeLeavingTheWorkspace(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	file := filepath.Join(outside, "月报.xlsx")
	args := `{"relative_path":"` + jsonPath(file) + `"}`
	stage := PathPolicyStage{}

	if d := stage.Evaluate(pathCtx(ws, "balanced", `{"relative_path":"data/a.xlsx","save_as":"out/b.xlsx"}`)); d != nil {
		t.Errorf("paths inside the workspace need nothing: %+v", d)
	}

	d := stage.Evaluate(pathCtx(ws, "balanced", args))
	if d == nil || !d.Allowed || !d.RequiresApproval || len(d.GrantDirs) != 1 || d.GrantDirs[0] != outside {
		t.Fatalf("an outside path must be held for approval and name the folder to grant: %+v", d)
	}
	if !strings.Contains(d.Reason, file) || !strings.Contains(d.Reason, outside) {
		t.Errorf("the reason must show the user what is asked: %q", d.Reason)
	}

	// Every file argument counts, not only "path": save_as can leave the workspace too.
	d = stage.Evaluate(pathCtx(ws, "balanced", `{"relative_path":"a.xlsx","save_as":"`+jsonPath(file)+`"}`))
	if d == nil || !d.RequiresApproval {
		t.Errorf("save_as outside the workspace must be asked about: %+v", d)
	}
	// A folder argument grants the folder, not its parent.
	d = stage.Evaluate(pathCtx(ws, "open", `{"cwd":"`+jsonPath(outside)+`"}`))
	if d == nil || len(d.GrantDirs) != 1 || d.GrantDirs[0] != outside {
		t.Errorf("a directory argument grants itself: %+v", d)
	}

	// Never grantable, and never asked about.
	for name, c := range map[string]ToolPolicyContext{
		"strict mode":      pathCtx(ws, "strict", args),
		"traversal":        pathCtx(ws, "balanced", `{"relative_path":"..\\..\\x.xlsx"}`),
		"drive root":       pathCtx(ws, "balanced", `{"cwd":"`+jsonPath(filepath.VolumeName(ws)+string(filepath.Separator))+`"}`),
		"credential store": pathCtx(ws, "balanced", `{"path":"`+jsonPath(filepath.Join(mustHome(t), ".ssh", "id_rsa"))+`"}`),
	} {
		if d := stage.Evaluate(c); d == nil || d.Allowed {
			t.Errorf("%s must be refused: %+v", name, d)
		}
	}
}

func mustHome(t *testing.T) string {
	t.Helper()
	h, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	return h
}

func TestApprovingAnOutsidePathGrantsItsFolderForTheRun(t *testing.T) {
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	ws, outside := t.TempDir(), t.TempDir()
	t.Cleanup(func() { pathgrant.Default.Revoke(ws) })

	mw := NewGovernanceMiddleware(queue, BuildPolicy("balanced", ws))
	var seen []string
	wrapped, err := mw.WrapInvokableToolCall(context.Background(), func(_ context.Context, args string, _ ...tool.Option) (string, error) {
		seen = append(seen, args)
		return "ok", nil
	}, &adk.ToolContext{Name: "read_xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := `{"relative_path":"` + jsonPath(filepath.Join(outside, "a.xlsx")) + `"}`

	if _, err := wrapped(ctx, first); err == nil {
		t.Fatal("the first outside access must wait for approval")
	}
	if len(seen) != 0 || pathgrant.Default.Allowed(ws, filepath.Join(outside, "a.xlsx")) {
		t.Fatal("nothing runs and nothing is granted before the user answers")
	}
	pending, _ := queue.ListPending(ctx, nil)
	if len(pending) != 1 || !strings.Contains(pending[0].Prompt, outside) {
		t.Fatalf("one pending request that names the folder: %+v", pending)
	}
	if err := queue.Resolve(ctx, pending[0].ID, true, "ok", "tester", &ResumePayload{Approved: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := wrapped(ctx, first); err != nil {
		t.Fatal(err)
	}
	if !pathgrant.Default.Allowed(ws, filepath.Join(outside, "other", "b.xlsx")) {
		t.Fatal("approving grants the folder and what is below it")
	}

	// Another file in the folder now goes straight through; another folder still asks.
	if _, err := wrapped(ctx, `{"relative_path":"`+jsonPath(filepath.Join(outside, "c.xlsx"))+`"}`); err != nil {
		t.Errorf("a granted folder must not ask again: %v", err)
	}
	if _, err := wrapped(ctx, `{"relative_path":"`+jsonPath(filepath.Join(t.TempDir(), "d.xlsx"))+`"}`); err == nil {
		t.Error("a different folder must ask again")
	}
	if len(seen) != 2 {
		t.Errorf("the tool ran %d times, want 2: %v", len(seen), seen)
	}
}

func TestRejectingAnOutsidePathGrantsNothing(t *testing.T) {
	queue, err := NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	ws, outside := t.TempDir(), t.TempDir()
	mw := NewGovernanceMiddleware(queue, BuildPolicy("balanced", ws))
	ran := false
	wrapped, _ := mw.WrapInvokableToolCall(context.Background(), func(context.Context, string, ...tool.Option) (string, error) {
		ran = true
		return "ok", nil
	}, &adk.ToolContext{Name: "read_xlsx"})
	ctx := context.Background()
	args := `{"relative_path":"` + jsonPath(filepath.Join(outside, "a.xlsx")) + `"}`
	_, _ = wrapped(ctx, args)
	pending, _ := queue.ListPending(ctx, nil)
	if len(pending) != 1 {
		t.Fatalf("pending = %d", len(pending))
	}
	_ = queue.Resolve(ctx, pending[0].ID, false, "no", "tester", &ResumePayload{Approved: false})
	if ran || pathgrant.Default.Allowed(ws, filepath.Join(outside, "a.xlsx")) {
		t.Error("a rejected request must neither run the tool nor grant the folder")
	}
}
