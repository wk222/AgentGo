package agent

import (
	"agentgo/internal/processutil"
	"bytes"
	"context"
	"github.com/cloudwego/eino/adk/backgroundtask"
	backgroundshell "github.com/cloudwego/eino/adk/backgroundtask/shell"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBackgroundOutputCannotBlockWithoutUpdateConsumer(t *testing.T) {
	run := newOSCommandRun("verbose", "", "")
	done := make(chan struct{})
	go func() {
		io.Copy(&commandOutputWriter{run: run, kind: "stdout"}, bytes.NewReader(bytes.Repeat([]byte("x"), 4*processutil.MaxOutputBytes)))
		run.finishWith(nil, nil, backgroundtask.StatusCompleted)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		run.pipeR.Close()
		t.Fatal("output blocked because nobody consumed Updates")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := run.Wait(ctx)
	if err != nil || len(out.Data) > processutil.MaxOutputBytes+200 || !strings.Contains(string(out.Data), "output truncated") {
		t.Fatalf("unbounded or unmarked output size=%d err=%v", len(out.Data), err)
	}
	// Wait returns its own snapshot; callers cannot mutate the stored result.
	out.Data[0] = '!'
	again, err := run.Wait(ctx)
	if err != nil || again.Data[0] != 'x' {
		t.Fatal("caller mutated stored output", err)
	}
	run.pipeR.Close()
}

func TestBackgroundLargeOutputHelper(t *testing.T) {
	if os.Getenv("AGENTGO_TEST_LARGE_OUTPUT") != "1" {
		return
	}
	_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), 3*processutil.MaxOutputBytes))
	os.Exit(0)
}
func TestBackgroundQuickVerboseCommandFullyDrainsOutput(t *testing.T) {
	t.Setenv("AGENTGO_TEST_LARGE_OUTPUT", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "\"" + exe + "\" -test.run=^TestBackgroundLargeOutputHelper$"
	if runtime.GOOS != "windows" {
		command = "'" + strings.ReplaceAll(exe, "'", "'\"'\"'") + "' -test.run=^TestBackgroundLargeOutputHelper$"
	}
	shell := NewOSRecoverableShell(t.TempDir())
	defer shell.Close(time.Second)
	run, err := shell.StartCommand(context.Background(), &backgroundshell.StartCommandRequest{TaskID: "large", Command: command})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := run.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != backgroundtask.StatusCompleted || !strings.Contains(string(out.Data), strings.Repeat("x", 100)) || len(out.Data) < processutil.MaxOutputBytes || len(out.Data) > processutil.MaxOutputBytes+200 || !strings.Contains(string(out.Data), "output truncated") {
		t.Fatalf("status=%s length=%d error=%q", out.Status, len(out.Data), out.Error)
	}
	run.(*osCommandRun).Updates().Close()
}

func TestUnknownBackgroundRecoveryNeverRestartsCommand(t *testing.T) {
	shell := NewOSRecoverableShell(t.TempDir())
	defer shell.Close(time.Second)
	_, err := shell.RecoverCommand(context.Background(), &backgroundshell.RecoverCommandRequest{TaskID: "lost-before-restart", Command: "echo should-not-run"})
	if err == nil || !strings.Contains(err.Error(), "side effects are unknown") {
		t.Fatal("unknown command was replayed", err)
	}
	if len(shell.runs) != 0 {
		t.Fatal("recovery started an untracked command")
	}
}
func TestCompletedBackgroundCommandIsNotExecutedTwice(t *testing.T) {
	shell := NewOSRecoverableShell(t.TempDir())
	defer shell.Close(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := &backgroundshell.StartCommandRequest{TaskID: "once", Command: "echo once"}
	first, err := shell.StartCommand(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := first.Wait(ctx); err != nil || out.Status != backgroundtask.StatusCompleted {
		t.Fatal(out, err)
	}
	second, err := shell.StartCommand(ctx, req)
	if err != nil || second != first {
		t.Fatal("completed command restarted", err)
	}
	if _, err = shell.StartCommand(ctx, &backgroundshell.StartCommandRequest{TaskID: "once", Command: "echo other"}); err == nil {
		t.Fatal("accepted task ID collision")
	}
}
