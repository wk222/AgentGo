package shellcmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProcessTreeContextCancellationEndsDescendants(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := Command(ctx, slowGrandchild())
	cmd.Dir = dir
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Kill()
	done := make(chan error, 1)
	go func() { done <- tree.Wait() }()
	if !waitFor(filepath.Join(dir, "started.txt"), 15*time.Second) {
		t.Fatal("child never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled command reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("context cancellation did not complete")
	}
	if waitFor(filepath.Join(dir, "marker.txt"), 4*time.Second) {
		t.Fatal("descendant survived cancellation")
	}
}

func TestProcessTreeCancelledBeforeStartHasNoSideEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	cmd := Command(ctx, slowGrandchild())
	cmd.Dir = dir
	tree, err := StartTree(cmd)
	if err == nil {
		tree.Kill()
		tree.Wait()
		t.Fatal("cancelled command started")
	}
	if _, err = os.Stat(filepath.Join(dir, "started.txt")); !os.IsNotExist(err) {
		t.Fatal("cancelled command had side effects", err)
	}
}
