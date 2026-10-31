package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// longRunning is a command that would take half a minute.
func longRunning() string {
	if runtime.GOOS == "windows" {
		return "ping -n 30 127.0.0.1 > nul"
	}
	return "sleep 30"
}

func TestRunShellReportsOutputAndExitStatus(t *testing.T) {
	out, err := runShell(context.Background(), t.TempDir(), "echo hello", 0)
	if err != nil || out.ExitCode != 0 || !strings.Contains(out.Stdout, "hello") {
		t.Fatalf("%+v, err %v", out, err)
	}
	out, err = runShell(context.Background(), t.TempDir(), "exit 3", 0)
	if err != nil || out.ExitCode != 3 {
		t.Fatalf("exit 3: %+v, err %v", out, err)
	}
}

func TestRunShellRunsInTheGivenDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	list := "dir /b"
	if runtime.GOOS != "windows" {
		list = "ls"
	}
	out, err := runShell(context.Background(), dir, list, 0)
	if err != nil || !strings.Contains(out.Stdout, "marker.txt") {
		t.Fatalf("%+v, err %v", out, err)
	}
}

func TestRunShellStopsACommandThatRunsOutOfTime(t *testing.T) {
	begin := time.Now()
	out, err := runShell(context.Background(), t.TempDir(), longRunning(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begin); took > 8*time.Second {
		t.Fatalf("a 1s limit let the command run %v", took)
	}
	if out.ExitCode != shellTimeoutStatus || !strings.Contains(out.Stderr, "[timeout]") || !strings.Contains(out.Stderr, "1 seconds") {
		t.Fatalf("a timed out command must say so: %+v", out)
	}
}

func TestRunShellStopsWhenTheCallerCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(700*time.Millisecond, cancel)
	begin := time.Now()
	_, err := runShell(ctx, t.TempDir(), longRunning(), time.Minute)
	if err == nil {
		t.Fatal("a cancelled run reported success")
	}
	if took := time.Since(begin); took > 8*time.Second {
		t.Fatalf("cancelling left the command running for %v", took)
	}
}

func TestRunShellKeepsHugeOutputBounded(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("x", 1023) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat(line, 20*1024)), 0o600); err != nil { // 20 MiB
		t.Fatal(err)
	}
	show := "type big.txt"
	if runtime.GOOS != "windows" {
		show = "cat big.txt"
	}
	out, err := runShell(context.Background(), dir, show, 0)
	if err != nil || out.ExitCode != 0 {
		t.Fatalf("%v, status %d", err, out.ExitCode)
	}
	if len(out.Stdout) > shellOutputCap*3/2+200 || !strings.Contains(out.Stdout, "bytes of output dropped") {
		t.Fatalf("20 MiB of output came back as %d bytes without a marker", len(out.Stdout))
	}
}

func TestShellTimeoutResolution(t *testing.T) {
	t.Setenv("AGENTGO_SHELL_TIMEOUT_SEC", "")
	if got := shellTimeout(0); got != 180*time.Second {
		t.Fatalf("default %v", got)
	}
	if got := shellTimeout(60); got != time.Minute {
		t.Fatalf("requested 60s gave %v", got)
	}
	if got := shellTimeout(1 << 20); got != maxShellTimeout {
		t.Fatalf("an absurd request gave %v", got)
	}
	t.Setenv("AGENTGO_SHELL_TIMEOUT_SEC", "5")
	if got := shellTimeout(0); got != 5*time.Second {
		t.Fatalf("configured default gave %v", got)
	}
	t.Setenv("AGENTGO_SHELL_TIMEOUT_SEC", "0")
	if got := shellTimeout(0); got != 0 {
		t.Fatalf("0 must mean no default limit, got %v", got)
	}
	if got := shellTimeout(10); got != 10*time.Second {
		t.Fatalf("an explicit request still applies, got %v", got)
	}
}

func TestShellTimeoutNoteTellsTheModel(t *testing.T) {
	t.Setenv("AGENTGO_SHELL_TIMEOUT_SEC", "")
	if note := shellTimeoutNote(); !strings.Contains(note, "180 seconds") || !strings.Contains(note, "timeout_seconds") || !strings.Contains(note, "1800") {
		t.Fatalf("note: %q", note)
	}
	t.Setenv("AGENTGO_SHELL_TIMEOUT_SEC", "0")
	if note := shellTimeoutNote(); !strings.Contains(note, "no time limit") {
		t.Fatalf("note: %q", note)
	}
}
