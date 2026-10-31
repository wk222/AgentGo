//go:build windows

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// sbxWorkspace returns a fresh folder under the user's cache directory: `go test`
// points TEMP at GOTMPDIR, and a disk outside the profile usually does not let
// the user label folders (which the Windows sandbox needs).
func sbxWorkspace(t *testing.T) string {
	t.Helper()
	base, err := os.UserCacheDir()
	if err != nil {
		t.Skip(err)
	}
	root := filepath.Join(base, "agentgo", "sandbox-tests")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "tools")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
		os.Remove(root) // only if now empty
	})
	return dir
}

func sbxText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return strings.TrimSpace(string(b))
}

// The whole path an agent's command takes on Windows: the flags, runShell, the
// sandbox, shellcmd's process tree with the options runShell really uses.
func TestRunShellWindowsSandboxEndToEnd(t *testing.T) {
	t.Setenv("AGENTGO_SANDBOX_OS", "1")
	ws, outside := sbxWorkspace(t), sbxWorkspace(t)
	victim := filepath.Join(outside, "victim.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without acknowledging the open network the command must not run at all.
	t.Setenv("AGENTGO_SANDBOX_OS_NET", "")
	out, err := runShell(context.Background(), ws, "echo should-not-run> marker.txt", 0)
	if err == nil {
		t.Fatalf("expected a refusal, got %+v", out)
	}
	if !strings.Contains(err.Error(), "AGENTGO_SANDBOX_OS_NET") {
		t.Fatalf("the refusal must say how to proceed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(ws, "marker.txt")); statErr == nil {
		t.Fatal("the refused command ran anyway")
	}

	// Acknowledged: writes in the workspace work, writes outside are refused.
	t.Setenv("AGENTGO_SANDBOX_OS_NET", "1")
	out, err = runShell(context.Background(), ws, `echo inside> inside.txt && echo hacked> "`+victim+`"`, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Skipf("cannot label folders here: %v", err)
		}
		t.Fatalf("runShell: %v", err)
	}
	if got := sbxText(filepath.Join(ws, "inside.txt")); got != "inside" {
		t.Errorf("inside.txt = %q (exit %d, stderr %q)", got, out.ExitCode, out.Stderr)
	}
	if got := sbxText(victim); got != "keep" {
		t.Errorf("a file outside the workspace changed: %q", got)
	}
	if out.ExitCode == 0 {
		t.Errorf("the write outside the workspace should have failed, stdout %q", out.Stdout)
	}
}
