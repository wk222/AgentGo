package shellcmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedDescendantHelper(t *testing.T) {
	if os.Getenv("AGENTGO_OWNED_TREE_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "child" {
		if err := os.WriteFile("started.txt", []byte("started"), 0600); err != nil {
			os.Exit(8)
		}
		time.Sleep(3 * time.Second)
		os.WriteFile("marker.txt", []byte("survived"), 0600)
		os.Exit(0)
	}
	if mode != "parent" {
		os.Exit(9)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnedDescendantHelper$", "--", "child")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		os.Exit(10)
	}
	if !waitFor("started.txt", 10*time.Second) {
		os.Exit(11)
	}
	// Deliberately exit without waiting for the child: ownership must survive
	// the loss of the root PID, even when its child holds the output pipe.
	os.Exit(0)
}

func TestProcessTreeOwnsChildrenAfterRootExit(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnedDescendantHelper$", "--", "parent")
	cmd.Env = append(os.Environ(), "AGENTGO_OWNED_TREE_HELPER=1")
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = 150 * time.Millisecond
	tree, err := StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Kill()
	done := make(chan error, 1)
	go func() { done <- tree.Wait() }()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
			t.Fatalf("%v: %s", err, output.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("orphan held output pipe open")
	}
	if _, err := os.Stat(filepath.Join(dir, "started.txt")); err != nil {
		t.Fatal("child was never started", err)
	}
	if waitFor(filepath.Join(dir, "marker.txt"), 4*time.Second) {
		t.Fatal("child survived its owned root")
	}
}
