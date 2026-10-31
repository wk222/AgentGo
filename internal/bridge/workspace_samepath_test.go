package bridge

import (
	"path/filepath"
	"testing"

	"agentgo/internal/linktest"
)

// The runtime keeps the resolved root; a caller may name the same folder
// through a link or a short Windows name. That is not a different workspace.
func TestSameWorkspacePathSeesThroughALink(t *testing.T) {
	a, b := markedWorkspace(t, "ALPHA"), markedWorkspace(t, "BRAVO")
	link := filepath.Join(t.TempDir(), "alpha-link")
	linktest.Dir(t, link, a)

	if !sameWorkspacePath(link, a) || !sameWorkspacePath(a, link) {
		t.Errorf("a link and its folder are the same workspace: %q %q", link, a)
	}
	if sameWorkspacePath(link, b) {
		t.Error("another folder is another workspace")
	}
}

// SetWorkspaceRoot resolves what it is given, so the new root is always the real
// path; the root the runtime already holds is whatever spelling it was started
// with. Choosing the folder that is already open must be a no-op either way.
func TestSwitchingToTheSameWorkspaceUnderAnotherSpellingDoesNotRestartTools(t *testing.T) {
	a := markedWorkspace(t, "ALPHA")
	link := filepath.Join(t.TempDir(), "alpha-link")
	linktest.Dir(t, link, a)
	s, _, _ := newCodetoolsService(t, link) // the runtime holds the link's spelling
	calls := 0
	s.rt.AddWorkspaceBinder(func(string) error { calls++; return nil })

	if _, err := s.rt.SetWorkspaceRoot(a); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("binders ran %d times for a switch to the folder already open", calls)
	}
}
