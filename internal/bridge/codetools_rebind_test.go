package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/governance"
	"agentgo/internal/plugin"
	"agentgo/internal/tools"
)

// F1 acceptance: switching workspaces moves the code tools with it, atomically.

func markedWorkspace(t *testing.T, marker string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte(marker+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// viewMarker reads marker.txt through the registered code_view tool using a
// relative path, which resolves against whatever workspace the tool is bound to.
func viewMarker(t *testing.T, reg *tools.Registry) string {
	t.Helper()
	return invoke(t, registryTool(t, reg, "code_view"), map[string]any{"file_path": "marker.txt"})
}

func countTool(reg *tools.Registry, name string) int {
	n := 0
	for _, bt := range reg.GetAllTools() {
		if info, _ := bt.Info(context.Background()); info != nil && info.Name == name {
			n++
		}
	}
	return n
}

func TestWorkspaceSwitchMovesCodetools(t *testing.T) {
	a, b := markedWorkspace(t, "ALPHA"), markedWorkspace(t, "BRAVO")
	s, reg, _ := newCodetoolsService(t, a)

	if out := viewMarker(t, reg); !strings.Contains(out, "ALPHA") {
		t.Fatalf("before switch: %s", out)
	}
	for i, ws := range []string{b, a, b} { // repeated switches must not stack registrations
		if _, err := s.rt.SetWorkspaceRoot(ws); err != nil {
			t.Fatalf("switch %d: %v", i, err)
		}
		want := "BRAVO"
		if ws == a {
			want = "ALPHA"
		}
		if out := viewMarker(t, reg); !strings.Contains(out, want) {
			t.Fatalf("switch %d: tools still bound to the old workspace:\n%s", i, out)
		}
		for _, name := range []string{"code_view", "code_edit", "lsp_diagnostics"} {
			if n := countTool(reg, name); n != 1 {
				t.Fatalf("switch %d: %d registrations of %s", i, n, name)
			}
		}
	}
	if st := pluginState(t, s, "codetools"); st != plugin.StateRunning {
		t.Fatalf("codetools state = %s", st)
	}
}

func TestFailedWorkspaceSwitchKeepsOldWorkspaceUsable(t *testing.T) {
	a, b := markedWorkspace(t, "ALPHA"), markedWorkspace(t, "BRAVO")
	s, reg, _ := newCodetoolsService(t, a)

	// A later binder vetoes the switch, after codetools already moved to B.
	veto := errors.New("veto")
	s.rt.AddWorkspaceBinder(func(root string) error {
		if sameWorkspacePath(root, b) {
			return veto
		}
		return nil
	})

	_, err := s.rt.SetWorkspaceRoot(b)
	if !errors.Is(err, veto) {
		t.Fatalf("err = %v, want the veto", err)
	}
	if got := s.rt.WorkspaceRoot(); !sameWorkspacePath(got, a) {
		t.Fatalf("workspace = %s, want rollback to %s", got, a)
	}
	if out := viewMarker(t, reg); !strings.Contains(out, "ALPHA") {
		t.Fatalf("tools not restored to the old workspace:\n%s", out)
	}
	if n := countTool(reg, "code_view"); n != 1 {
		t.Fatalf("%d registrations of code_view after rollback", n)
	}
	if st := pluginState(t, s, "codetools"); st != plugin.StateRunning {
		t.Fatalf("codetools state = %s", st)
	}
}

func TestWorkspaceSwitchRefusedWhileApprovalPending(t *testing.T) {
	a, b := markedWorkspace(t, "ALPHA"), markedWorkspace(t, "BRAVO")
	s, reg, _ := newCodetoolsService(t, a)
	s.rt.pending = newPendingStore()
	s.rt.pending.Set(pendingRun{ApprovalID: "appr_1", ToolName: "code_edit"})

	_, err := s.rt.SetWorkspaceRoot(b)
	if !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("err = %v, want ErrWorkspaceBusy", err)
	}
	if got := s.rt.WorkspaceRoot(); !sameWorkspacePath(got, a) {
		t.Fatalf("workspace changed to %s while busy", got)
	}
	if out := viewMarker(t, reg); !strings.Contains(out, "ALPHA") {
		t.Fatalf("tools moved while busy:\n%s", out)
	}
	if r := s.SetWorkspaceRoot(b); r["success"] != false {
		t.Fatalf("AppService must surface the refusal: %v", r)
	}

	s.rt.pending.Delete("appr_1")
	if _, err := s.rt.SetWorkspaceRoot(b); err != nil {
		t.Fatalf("after resolving: %v", err)
	}
	if out := viewMarker(t, reg); !strings.Contains(out, "BRAVO") {
		t.Fatalf("after resolving:\n%s", out)
	}
}

// The plugin declares which of its tools change files; governance reads that at
// decision time. It must follow the plugin's lifecycle, including a reload.
func TestCodetoolsTraitsFollowThePluginLifecycle(t *testing.T) {
	a, b := markedWorkspace(t, "ALPHA"), markedWorkspace(t, "BRAVO")
	s, _, _ := newCodetoolsService(t, a)

	check := func(when string, wantRegistered bool) {
		t.Helper()
		for name, wantMutating := range map[string]bool{"code_edit": true, "code_write": true, "code_view": false} {
			tr, ok := governance.ToolTraitsOf(name)
			if ok != wantRegistered {
				t.Fatalf("%s: traits of %s registered=%v, want %v", when, name, ok, wantRegistered)
			}
			if ok && (tr.Mutating != wantMutating || tr.Source != "codetools") {
				t.Fatalf("%s: traits of %s = %+v", when, name, tr)
			}
		}
	}
	check("after start", true)
	if _, err := s.rt.SetWorkspaceRoot(b); err != nil {
		t.Fatal(err)
	}
	check("after a workspace switch (reload)", true)
	s.StopPlugin("codetools")
	check("after stop", false)
}

func TestSwitchingToTheSameWorkspaceDoesNotRestartTools(t *testing.T) {
	a := markedWorkspace(t, "ALPHA")
	s, _, _ := newCodetoolsService(t, a)
	calls := 0
	s.rt.AddWorkspaceBinder(func(string) error { calls++; return nil })
	if _, err := s.rt.SetWorkspaceRoot(a); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("binders ran %d times for a no-op switch", calls)
	}
}

func TestStoppedCodetoolsStaysStoppedAcrossSwitch(t *testing.T) {
	a, b := markedWorkspace(t, "ALPHA"), markedWorkspace(t, "BRAVO")
	s, reg, _ := newCodetoolsService(t, a)
	s.StopPlugin("codetools")
	if _, err := s.rt.SetWorkspaceRoot(b); err != nil {
		t.Fatal(err)
	}
	if st := pluginState(t, s, "codetools"); st != plugin.StateStopped {
		t.Fatalf("a plugin the user stopped must stay stopped, got %s", st)
	}
	if n := countTool(reg, "code_view"); n != 0 {
		t.Fatalf("%d code_view registrations while stopped", n)
	}
}

func TestClosedServiceIsUnboundFromWorkspaceSwitches(t *testing.T) {
	a, b := markedWorkspace(t, "ALPHA"), markedWorkspace(t, "BRAVO")
	s, _, _ := newCodetoolsService(t, a)
	s.Close()
	if n := len(s.rt.wsBinders); n != 0 {
		t.Fatalf("%d binders left after Close", n)
	}
	if _, err := s.rt.SetWorkspaceRoot(b); err != nil {
		t.Fatalf("a closed service must not break later switches: %v", err)
	}
}
