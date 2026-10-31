package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceDirectorySelectionCancel(t *testing.T) {
	r := workspaceDirectorySelection(" ")
	if r["success"] != true || r["cancelled"] != true || r["path"] != "" {
		t.Fatal(r)
	}
}
func TestWorkspaceDirectorySelectionValidatesDirectory(t *testing.T) {
	root := t.TempDir()
	r := workspaceDirectorySelection(root)
	if r["success"] != true || r["cancelled"] != false || r["path"] == "" {
		t.Fatal(r)
	}
	file := filepath.Join(root, "file.txt")
	_ = os.WriteFile(file, []byte("test"), 0600)
	if workspaceDirectorySelection(file)["success"] != false {
		t.Fatal("file accepted as directory")
	}
	if workspaceDirectorySelection(filepath.Join(root, "missing"))["success"] != false {
		t.Fatal("nonexistent directory accepted")
	}
}
func TestWorkspaceDirectorySelectionNativeCancellation(t *testing.T) {
	if !workspaceDialogCancelled(errors.New("cancelled by user")) || !workspaceDialogCancelled(fmt.Errorf("dialog: %w", errors.New("cancelled by user"))) {
		t.Fatal("native cancellation not recognized")
	}
	if workspaceDialogCancelled(errors.New("access denied")) || workspaceDialogCancelled(nil) {
		t.Fatal("real error mistaken for cancellation")
	}
}
