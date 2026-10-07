package sandbox

import (
	"os"
	"strings"
)

// DefaultSeam returns the configured execution seam (defaults to LocalExecutionSeam).
func DefaultSeam() ExecutionSeam {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("AGENTGO_SANDBOX_MODE")))
	switch Mode(mode) {
	case ModeDocker:
		// Fallback to local if docker is not running
		return NewLocalExecutionSeam()
	default:
		return NewLocalExecutionSeam()
	}
}
