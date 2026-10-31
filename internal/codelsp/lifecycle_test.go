package codelsp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Run the test binary as an intentionally unresponsive LSP server. No shell,
// external server, or user workspace is needed to reproduce shutdown mid-init.
func TestStalledServerHelper(t *testing.T) {
	if os.Getenv("AGENTGO_TEST_STALLED_LSP") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestStopAllJoinsStartingServerAndCanRestart(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	m := NewManager(ManagerOptions{WorkDir: root, NoAuto: true, Overrides: Overrides{
		"test-stalled": {Command: executable, Args: []string{"-test.run=^TestStalledServerHelper$"},
			Env: map[string]string{"AGENTGO_TEST_STALLED_LSP": "1"}, FileTypes: []string{"teststall"}, TimeoutSec: 30},
	}})
	t.Cleanup(func() { m.StopAll(context.Background()) })
	for round := 0; round < 2; round++ {
		done := make(chan struct{})
		go func() { defer close(done); m.Start(context.Background(), filepath.Join(root, "file.teststall")) }()
		deadline := time.After(5 * time.Second)
		for {
			if c, ok := m.Client("test-stalled"); ok && c.State() == StateStarting {
				break
			}
			select {
			case <-deadline:
				t.Fatal("server never entered initialization")
			case <-time.After(10 * time.Millisecond):
			}
		}
		stopped := make(chan struct{})
		go func() { m.StopAll(context.Background()); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown did not cancel initialization")
		}
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("startup survived shutdown")
		}
		if len(m.Clients()) != 0 {
			t.Fatal("server published after shutdown")
		}
	}
}

func TestClosedManagerCannotLaunchServer(t *testing.T) {
	m := NewManager(ManagerOptions{WorkDir: t.TempDir(), NoAuto: true})
	m.Close(context.Background())
	m.Start(context.Background(), filepath.Join(m.workDir, "file.go"))
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed || len(m.clients) != 0 {
		t.Fatal("closed manager accepted new work")
	}
}
