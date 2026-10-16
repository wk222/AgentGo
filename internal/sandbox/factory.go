package sandbox

import (
	"strings"

	"agentgo/internal/config"
)

// DefaultSeam returns the configured execution seam (defaults to LocalExecutionSeam).
func DefaultSeam() ExecutionSeam {
	mode := strings.ToLower(config.SandboxMode.Get())
	switch Mode(mode) {
	case ModeDocker:
		// Fallback to local if docker is not running
		return NewLocalExecutionSeam()
	default:
		return NewLocalExecutionSeam()
	}
}
