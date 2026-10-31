package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/pathgrant"
)

func TestWorkspaceFileJailAcceptsOnlyApprovedOutsideFolders(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	t.Cleanup(func() { pathgrant.Default.Revoke(ws) })
	if err := os.WriteFile(filepath.Join(ws, "in.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "out.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	// An absolute path that points inside the workspace is just a file of it.
	got, err := ResolveWorkspaceFile(ws, filepath.Join(ws, "in.txt"))
	if err != nil || filepath.Base(got) != "in.txt" {
		t.Fatalf("absolute path inside the workspace: %q, %v", got, err)
	}

	target := filepath.Join(outside, "out.txt")
	if _, err := ResolveWorkspaceFile(ws, target); err == nil || !strings.Contains(err.Error(), "approved") {
		t.Fatalf("an outside path nobody approved must be refused: %v", err)
	}

	pathgrant.Default.Grant(ws, outside)
	got, err = ResolveWorkspaceFile(ws, target)
	if err != nil || filepath.Base(got) != "out.txt" {
		t.Fatalf("after approval: %q, %v", got, err)
	}
	// A file that does not exist yet (the output of save_as) may be created in the folder.
	if got, err = ResolveWorkspaceFile(ws, filepath.Join(outside, "new", "result.xlsx")); err != nil || filepath.Base(got) != "result.xlsx" {
		t.Fatalf("new file in a granted folder: %q, %v", got, err)
	}
	// The grant stops at the folder: siblings, parents and traversal stay closed.
	for name, p := range map[string]string{
		"sibling":   filepath.Join(t.TempDir(), "x.txt"),
		"parent":    filepath.Join(filepath.Dir(outside), "x.txt"),
		"traversal": filepath.Join(outside, "..", "x.txt"),
	} {
		if _, err := ResolveWorkspaceFile(ws, p); err == nil {
			t.Errorf("%s must stay closed", name)
		}
	}
	// A grant belongs to its workspace.
	if _, err := ResolveWorkspaceFile(t.TempDir(), target); err == nil {
		t.Error("another workspace has no grant")
	}
}
