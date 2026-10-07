package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/backgroundtask"
	backgroundshell "github.com/cloudwego/eino/adk/backgroundtask/shell"
)

// slowChildCommand starts a grandchild (not just the shell) that writes
// started.txt right away and marker.txt three seconds later.
func slowChildCommand() string {
	if runtime.GOOS == "windows" {
		return `powershell -NoProfile -Command "Set-Content -Path started.txt -Value x; Start-Sleep 3; Set-Content -Path marker.txt -Value x"`
	}
	return `(touch started.txt; sleep 3; touch marker.txt)`
}

func waitForFile(t *testing.T, path string, within time.Duration) bool {
	t.Helper()
	for deadline := time.Now().Add(within); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

func newCoordinator(t *testing.T) (*BackgroundTaskCoordinator, string) {
	t.Helper()
	ws := t.TempDir()
	c, err := NewBackgroundTaskCoordinator(ws, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, ws
}

// Task state is in memory only, so a command still running when AgentGo exits
// could never be picked up again. Close must end it, and what it started.
func TestCloseEndsRunningCommandsAndEverythingTheyStarted(t *testing.T) {
	c, ws := newCoordinator(t)
	run, err := c.Shell().(*OSRecoverableShell).StartCommand(context.Background(),
		&backgroundshell.StartCommandRequest{TaskID: "t1", Command: slowChildCommand()})
	if err != nil {
		t.Fatal(err)
	}
	if !waitForFile(t, filepath.Join(ws, "started.txt"), 15*time.Second) {
		t.Fatal("the grandchild never started; the test cannot tell anything")
	}

	begin := time.Now()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if d := time.Since(begin); d > 5*time.Second {
		t.Errorf("Close took %v; shutdown must not wait for commands", d)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("the run should be finished after Close: %v", err)
	}
	if out.Status != backgroundtask.StatusCanceled || !strings.Contains(out.Error, "interrupted") {
		t.Errorf("want canceled with the shutdown reason, got status=%s error=%q", out.Status, out.Error)
	}

	// The grandchild would have written marker.txt after 3 seconds.
	if waitForFile(t, filepath.Join(ws, "marker.txt"), 5*time.Second) {
		t.Fatal("a process started by the command survived Close")
	}
}

func TestShutdownIsIdempotentAndRefusesNewCommands(t *testing.T) {
	c, _ := newCoordinator(t)
	shell := c.Shell().(*OSRecoverableShell)
	done, err := shell.StartCommand(context.Background(), &backgroundshell.StartCommandRequest{TaskID: "quick", Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before, err := done.Wait(ctx)
	if err != nil || before.Status != backgroundtask.StatusCompleted {
		t.Fatalf("quick command: %+v %v", before, err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := shell.StartCommand(context.Background(), &backgroundshell.StartCommandRequest{TaskID: "late", Command: "echo late"}); err == nil {
		t.Fatal("a command must not start after shutdown")
	}
	after, _ := done.Wait(ctx)
	if after.Status != backgroundtask.StatusCompleted {
		t.Errorf("a command that had finished must keep its result, got %s", after.Status)
	}
}

// On Windows a command containing double quotes used to be mangled by argument
// escaping: cmd.exe echoed the text instead of running it.
func TestQuotedCommandsRunAsWritten(t *testing.T) {
	c, _ := newCoordinator(t)
	command := `echo "hello world"`
	if runtime.GOOS == "windows" {
		command = `powershell -NoProfile -Command "Write-Output 'hello world'"`
	}
	run, err := c.Shell().(*OSRecoverableShell).StartCommand(context.Background(),
		&backgroundshell.StartCommandRequest{TaskID: "q1", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := run.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out.Data)
	if out.Status != backgroundtask.StatusCompleted || !strings.Contains(got, "hello world") || strings.Contains(got, "Write-Output") {
		t.Fatalf("command was not run as written: status=%s out=%q err=%q", out.Status, got, out.Error)
	}
}

// Stop used to set "canceled" and then be overwritten with "failed" when the
// killed process reported its exit code.
func TestStopReportsCanceledNotFailed(t *testing.T) {
	c, ws := newCoordinator(t)
	run, err := c.Shell().(*OSRecoverableShell).StartCommand(context.Background(),
		&backgroundshell.StartCommandRequest{TaskID: "s1", Command: slowChildCommand()})
	if err != nil {
		t.Fatal(err)
	}
	if !waitForFile(t, filepath.Join(ws, "started.txt"), 15*time.Second) {
		t.Fatal("never started")
	}
	if err := run.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := run.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != backgroundtask.StatusCanceled {
		t.Errorf("status = %s, want canceled", out.Status)
	}
	if waitForFile(t, filepath.Join(ws, "marker.txt"), 5*time.Second) {
		t.Error("Stop left a child process running")
	}
}
