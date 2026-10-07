package ideruntime_test

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/ideruntime"
)

func TestProcessSupervisor_StdProcess(t *testing.T) {
	sup, err := ideruntime.NewProcessSupervisor()
	if err != nil {
		t.Fatalf("NewProcessSupervisor: %v", err)
	}
	defer sup.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := "cmd"
	args := []string{"/C", "echo", "hello-agentgo-runtime"}
	if runtime.GOOS != "windows" {
		cmd = "echo"
		args = []string{"hello-agentgo-runtime"}
	}

	spec := ideruntime.ProcessSpec{
		ID:      "test-proc-1",
		Kind:    ideruntime.KindTask,
		Command: cmd,
		Args:    args,
		UsePTY:  false,
	}

	var mu sync.Mutex
	var output strings.Builder

	proc, err := sup.Spawn(ctx, spec)
	if err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	proc.OnOutput(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		output.Write(data)
	})

	if err := proc.Wait(); err != nil {
		t.Fatalf("Wait error: %v", err)
	}

	if proc.ExitCode() != 0 {
		t.Errorf("expected exit code 0, got %d", proc.ExitCode())
	}

	mu.Lock()
	outStr := output.String()
	mu.Unlock()

	if !strings.Contains(outStr, "hello-agentgo-runtime") {
		t.Errorf("expected output to contain 'hello-agentgo-runtime', got %q", outStr)
	}
}

func TestProcessSupervisor_PTYProcess(t *testing.T) {
	sup, err := ideruntime.NewProcessSupervisor()
	if err != nil {
		t.Fatalf("NewProcessSupervisor: %v", err)
	}
	defer sup.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := "cmd.exe"
	args := []string{"/C", "echo", "pty-test-success"}
	if runtime.GOOS != "windows" {
		cmd = "sh"
		args = []string{"-c", "echo pty-test-success"}
	}

	spec := ideruntime.ProcessSpec{
		ID:      "test-pty-1",
		Kind:    ideruntime.KindTerminal,
		Command: cmd,
		Args:    args,
		UsePTY:  true,
		Cols:    80,
		Rows:    24,
	}

	var mu sync.Mutex
	var output strings.Builder

	proc, err := sup.Spawn(ctx, spec)
	if err != nil {
		t.Fatalf("Spawn PTY error: %v", err)
	}

	proc.OnOutput(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		output.Write(data)
	})

	_ = proc.Wait()

	mu.Lock()
	outStr := output.String()
	mu.Unlock()

	if !strings.Contains(outStr, "pty-test-success") {
		t.Errorf("expected PTY output to contain 'pty-test-success', got %q", outStr)
	}
}
