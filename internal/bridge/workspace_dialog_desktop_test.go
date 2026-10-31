//go:build !web

package bridge

import "testing"

// The browser build opens a real OS dialog instead, so this only applies to
// the Wails desktop build.
func TestWorkspaceDirectorySelectionRequiresDesktop(t *testing.T) {
	r := (&AppService{}).SelectWorkspaceDirectory()
	if r["success"] != false {
		t.Fatal("headless picker should return an explicit error")
	}
}
