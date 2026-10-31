package tools

import (
	"os"
	"path/filepath"
	"testing"

	"agentgo/internal/linktest"
	"agentgo/internal/pathgrant"
)

// The path jail hands back paths spelled by their real place. Whatever the
// caller shows or compares from them must be relative to the real root, and an
// approval must count no matter how the approved folder was named.

func TestSearchNamesFilesRelativeToTheWorkspaceWhicheverWayItIsReached(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "src", "main.go"), []byte("package main\n\nfunc answer() int { return 41 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "workspace-link")
	linktest.Dir(t, link, ws)

	for name, root := range map[string]string{"real path": ws, "through a link": link} {
		out, err := searchWorkspace(root, searchWorkspaceInput{Query: "answer"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if out.Count != 1 || out.Matches[0].Path != "src/main.go" {
			t.Errorf("%s: matches = %+v, want one match at src/main.go", name, out.Matches)
		}
	}
}

func TestApprovedFolderOpensWhicheverWayItIsNamed(t *testing.T) {
	ws := t.TempDir()
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "out.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	linktest.Dir(t, alias, real)
	wsLink := filepath.Join(t.TempDir(), "ws-link")
	linktest.Dir(t, wsLink, ws)
	t.Cleanup(func() { pathgrant.Default.Revoke(ws) })

	// The approval came in as the link; the workspace is opened through a link too.
	if !pathgrant.Default.Grant(wsLink, alias) {
		t.Fatal("the grant was refused")
	}
	for name, p := range map[string]string{
		"real path":        filepath.Join(real, "out.txt"),
		"through the link": filepath.Join(alias, "out.txt"),
		"new file":         filepath.Join(alias, "new", "result.xlsx"),
	} {
		for rootName, root := range map[string]string{"real workspace": ws, "linked workspace": wsLink} {
			if _, err := ResolveWorkspaceFile(root, p); err != nil {
				t.Errorf("%s in the %s: %v", name, rootName, err)
			}
		}
	}
	if _, err := ResolveWorkspaceFile(ws, filepath.Join(base, "x.txt")); err == nil {
		t.Error("the grant stops at the folder: its parent stays closed")
	}
}
