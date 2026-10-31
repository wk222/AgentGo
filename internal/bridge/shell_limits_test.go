package bridge

import (
	"testing"

	"agentgo/internal/shellcmd"
)

func TestApplyShellLimitsFollowsTheSettings(t *testing.T) {
	prev := shellcmd.DefaultLimits()
	t.Cleanup(func() { shellcmd.SetDefaultLimits(prev) })

	t.Setenv("AGENTGO_SHELL_MEM_MB", "768")
	t.Setenv("AGENTGO_SHELL_MAX_PROCS", "40")
	want := shellcmd.Limits{MemoryBytes: 768 << 20, MaxProcesses: 40}
	if got := applyShellLimits(); got != want || shellcmd.DefaultLimits() != want {
		t.Fatalf("applied %+v, in force %+v, want %+v", got, shellcmd.DefaultLimits(), want)
	}

	t.Setenv("AGENTGO_SHELL_MEM_MB", "0")
	t.Setenv("AGENTGO_SHELL_MAX_PROCS", "0")
	if got := applyShellLimits(); got != (shellcmd.Limits{}) {
		t.Fatalf("zero must switch both caps off, got %+v", got)
	}
}
