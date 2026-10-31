package bridge

import (
	"log"

	"agentgo/internal/config"
	"agentgo/internal/shellcmd"
)

// applyShellLimits gives every command the runtime starts the memory and process
// caps the settings ask for (AGENTGO_SHELL_MEM_MB, AGENTGO_SHELL_MAX_PROCS), so
// a runaway script or an accidental fork loop cannot take an office PC down.
// It returns what it set.
func applyShellLimits() shellcmd.Limits {
	limits := shellcmd.LimitsFromSettings(config.ShellMemoryMB.Get(), config.ShellMaxProcs.Get())
	shellcmd.SetDefaultLimits(limits)
	log.Printf("[agentgo] command limits: memory=%d MiB processes=%d (0 = unlimited)", limits.MemoryBytes>>20, limits.MaxProcesses)
	return limits
}
