package ideruntime_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/ideruntime"
)

func TestTerminalService_LifecycleAndInput(t *testing.T) {
	sup, err := ideruntime.NewProcessSupervisor()
	if err != nil {
		t.Fatalf("NewProcessSupervisor: %v", err)
	}
	defer sup.Close()

	termSvc := ideruntime.NewTerminalService(sup)

	var mu sync.Mutex
	var collected strings.Builder

	termSvc.OnTerminalData(func(termID string, data string) {
		mu.Lock()
		defer mu.Unlock()
		collected.WriteString(data)
	})

	t.Logf("Default shell is: %s", ideruntime.DefaultShell())
	info, err := termSvc.CreateTerminal("term-unit-1", "cmd.exe", "", 80, 24)
	if err != nil {
		t.Fatalf("CreateTerminal failed: %v", err)
	}
	t.Logf("Created terminal pid=%d", info.PID)
	if info.PID <= 0 {
		t.Errorf("expected positive PID, got %d", info.PID)
	}

	// Send an echo command
	time.Sleep(100 * time.Millisecond)
	_ = termSvc.WriteTerminal("term-unit-1", "echo terminal_stream_ok\r\n")

	// Wait for terminal output
	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		mu.Lock()
		text := collected.String()
		mu.Unlock()
		if strings.Contains(text, "terminal_stream_ok") {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !found {
		mu.Lock()
		got := collected.String()
		mu.Unlock()
		t.Errorf("expected output to contain 'terminal_stream_ok', got %q", got)
	}

	// Test Resize
	if err := termSvc.ResizeTerminal("term-unit-1", 100, 40); err != nil {
		t.Errorf("ResizeTerminal error: %v", err)
	}

	// Test Close
	if err := termSvc.CloseTerminal("term-unit-1"); err != nil {
		t.Errorf("CloseTerminal error: %v", err)
	}
}
