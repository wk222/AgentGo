//go:build windows

package workspace

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
)

// lockFileForTest makes a file unreadable for other handles until release is called.
func lockFileForTest(t *testing.T, path string) (release func()) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("cannot lock %s: %v", path, err)
	}
	var once sync.Once
	release = func() { once.Do(func() { _ = syscall.CloseHandle(h) }) }
	t.Cleanup(release)
	return release
}

// linkDirForTest points link at target. A symlink needs a privilege on Windows,
// so it falls back to a junction, which does not.
func linkDirForTest(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		t.Log("directory link created as a symlink")
		return
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("cannot create a directory link here: %v: %s", err, out)
	}
	t.Log("directory link created as a junction")
}
