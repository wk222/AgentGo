package bridge

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// A bad remembered workspace must not expose application data as an editable workspace.
func initialWorkspaceRoot(candidate, dataDir string) (string, error) {
	if normalized, err := normalizeWorkspaceRoot(candidate); err == nil {
		return normalized, nil
	} else {
		fallback := filepath.Join(dataDir, "workspace")
		log.Printf("[agentgo] workspace_root=%q invalid: %v; falling back to %s", candidate, err, fallback)
		if err := os.MkdirAll(fallback, 0700); err != nil {
			return "", fmt.Errorf("create fallback workspace: %w", err)
		}
		normalized, err := normalizeWorkspaceRoot(fallback)
		if err != nil {
			return "", fmt.Errorf("fallback workspace: %w", err)
		}
		return normalized, nil
	}
}
