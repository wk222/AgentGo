package ideruntime_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentgo/internal/ideruntime"
)

func mkdirs(t *testing.T, root string, rels ...string) {
	t.Helper()
	for _, r := range rels {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(r)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWatch_SkipsGeneratedDirs(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "src/a", "node_modules/x", "target/y", ".venv/z", "__pycache__", ".cache/c", "dist/d")

	fs, err := ideruntime.NewWorkspaceFS(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.StartWatcher(); err != nil {
		t.Fatal(err)
	}
	defer fs.StopWatcher()
	fs.WaitWatchIdle()

	// root + src + src/a only
	if got := fs.WatchedDirCount(); got != 3 {
		t.Fatalf("watched %d dirs, want 3 (generated/vendored trees must be skipped)", got)
	}
}

// A broad workspace must neither block StartWatcher nor register more than the cap.
func TestWatch_CapAndNonBlockingStart(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 120; i++ {
		mkdirs(t, root, fmt.Sprintf("d%03d/child", i))
	}

	fs, err := ideruntime.NewWorkspaceFS(root)
	if err != nil {
		t.Fatal(err)
	}
	fs.SetWatchLimits(25, 0) // zero sync budget: everything past the root goes to the background
	t0 := time.Now()
	if err := fs.StartWatcher(); err != nil {
		t.Fatal(err)
	}
	defer fs.StopWatcher()
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("StartWatcher blocked for %v", d)
	}
	fs.WaitWatchIdle()
	if got := fs.WatchedDirCount(); got != 25 {
		t.Fatalf("watched %d dirs, want exactly the cap 25", got)
	}
}

func TestWatch_SetRootDoesNotBlockAndRewatches(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for i := 0; i < 60; i++ {
		mkdirs(t, b, fmt.Sprintf("p%02d/q", i))
	}

	fs, err := ideruntime.NewWorkspaceFS(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.StartWatcher(); err != nil {
		t.Fatal(err)
	}
	defer fs.StopWatcher()
	fs.WaitWatchIdle()
	if got := fs.WatchedDirCount(); got != 1 {
		t.Fatalf("empty root should watch 1 dir, got %d", got)
	}

	t0 := time.Now()
	if err := fs.SetRoot(b); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("SetRoot blocked for %v", d)
	}
	fs.WaitWatchIdle()
	// root + 60 p?? + 60 q
	if got := fs.WatchedDirCount(); got != 121 {
		t.Fatalf("after SetRoot watched %d dirs, want 121", got)
	}
}
