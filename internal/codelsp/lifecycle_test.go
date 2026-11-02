package codelsp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Run the test binary as an intentionally unresponsive language server. No shell,
// external server or user workspace is needed to reproduce shutdown mid-startup.
func TestStalledServerHelper(t *testing.T) {
	if os.Getenv("AGENTGO_TEST_STALLED_LSP") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestStopAllJoinsAStartingServerAndTheSupervisorCanRestart(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	sv := newSupervisor(supervisorOptions{root: root, noAuto: true, overrides: Overrides{
		"test-stalled": {Command: exe, Args: []string{"-test.run=^TestStalledServerHelper$"},
			Env: map[string]string{"AGENTGO_TEST_STALLED_LSP": "1"}, FileTypes: []string{"teststall"}, TimeoutSec: 30},
	}})
	t.Cleanup(func() { sv.stopAll(context.Background()) })

	for round := 0; round < 2; round++ {
		launched := make(chan struct{})
		go func() { defer close(launched); sv.ensure(context.Background(), filepath.Join(root, "file.teststall")) }()

		deadline := time.After(5 * time.Second)
		for {
			if s, ok := sv.named("test-stalled"); ok && s.current() == StateStarting {
				break
			}
			select {
			case <-deadline:
				t.Fatal("the server never entered initialisation")
			case <-time.After(10 * time.Millisecond):
			}
		}

		stopped := make(chan struct{})
		go func() { sv.stopAll(context.Background()); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown did not cancel the initialisation")
		}
		select {
		case <-launched:
		case <-time.After(time.Second):
			t.Fatal("the launch outlived the shutdown")
		}
		if len(sv.sessions()) != 0 {
			t.Fatal("a server was published after shutdown")
		}
	}
}

func TestClosedSupervisorRefusesNewLaunches(t *testing.T) {
	sv := newSupervisor(supervisorOptions{root: t.TempDir(), noAuto: true})
	sv.close(context.Background())
	sv.ensure(context.Background(), filepath.Join(sv.root, "file.go"))
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if len(sv.live) != 0 {
		t.Fatal("a closed supervisor accepted new work")
	}
	sv.lifecycle.Lock()
	defer sv.lifecycle.Unlock()
	if !sv.closed {
		t.Fatal("close must be sticky")
	}
}

func TestPathsOutsideTheWorkspaceStartNothing(t *testing.T) {
	root := t.TempDir()
	sv := newSupervisor(supervisorOptions{root: root, overrides: Overrides{
		"any": {Command: "definitely-not-installed", FileTypes: []string{"txt"}},
	}})
	t.Cleanup(func() { sv.stopAll(context.Background()) })
	sv.ensure(context.Background(), filepath.Join(t.TempDir(), "elsewhere.txt"))
	if len(sv.sessions()) != 0 {
		t.Fatal("a file outside the workspace must not start a server")
	}
}
