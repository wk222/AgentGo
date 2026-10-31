package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk/backgroundtask"
	backgroundshell "github.com/cloudwego/eino/adk/backgroundtask/shell"
)

func TestFailedCommandWithSideEffectsIsNeverImplicitlyReplayed(t *testing.T) {
	dir := t.TempDir()
	shell := NewOSRecoverableShell(dir)
	defer shell.Close(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req := &backgroundshell.StartCommandRequest{TaskID: "failed-once", Command: "echo attempted >> attempts.txt"}
	req.Command += " & exit 7"
	// sh uses ';' for sequencing rather than cmd's '&'.
	if os.PathSeparator != '\\' {
		req.Command = "echo attempted >> attempts.txt; exit 7"
	}
	first, err := shell.StartCommand(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := first.Wait(ctx)
	if err != nil || outcome.Status != backgroundtask.StatusFailed {
		t.Fatal(outcome, err)
	}
	second, err := shell.StartCommand(ctx, req)
	if err != nil || second != first {
		t.Fatal("failed execution was restarted", err)
	}
	if _, err = second.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "attempts.txt"))
	if err != nil || strings.Count(string(content), "attempted") != 1 {
		t.Fatal("side effect was repeated", string(content), err)
	}
	if _, err = shell.RecoverCommand(ctx, &backgroundshell.RecoverCommandRequest{TaskID: req.TaskID, Command: "echo different"}); err == nil {
		t.Fatal("recovery attached to a different command")
	}
}

func TestCancelledStartRequestNeverCreatesExecution(t *testing.T) {
	shell := NewOSRecoverableShell(t.TempDir())
	defer shell.Close(time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := shell.StartCommand(ctx, &backgroundshell.StartCommandRequest{TaskID: "cancelled", Command: "echo should-not-run"}); err != context.Canceled {
		t.Fatal("cancelled request was accepted", err)
	}
	if len(shell.runs) != 0 {
		t.Fatal("cancelled request registered an execution")
	}
}

func TestCancellationAndCompletionPublishOneConsistentOutcome(t *testing.T) {
	for i := 0; i < 500; i++ {
		run := newOSCommandRun("race", "", "")
		begin := make(chan struct{})
		stopped := make(chan struct{})
		go func() { <-begin; run.halt(""); close(stopped) }()
		close(begin)
		run.finishWith(nil, nil, backgroundtask.StatusCompleted)
		<-stopped
		run.mu.Lock()
		accepted, status := run.stopped, run.status
		run.mu.Unlock()
		if accepted && status != backgroundtask.StatusCanceled {
			t.Fatalf("accepted stop overwritten by %s", status)
		}
		if !accepted && status != backgroundtask.StatusCompleted {
			t.Fatalf("finished outcome rewritten to %s", status)
		}
		run.Updates().Close()
	}
}
