//go:build !windows

package workspace

import (
	"os"
	"testing"
)

// lockFileForTest makes a file unreadable until release is called.
func lockFileForTest(t *testing.T, path string) (release func()) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can read files regardless of their mode")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	release = func() { _ = os.Chmod(path, 0o644) }
	t.Cleanup(release)
	return release
}

func linkDirForTest(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
}
