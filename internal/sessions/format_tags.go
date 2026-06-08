package sessions

import "fmt"

// FormatIsolationHint builds a tagged isolation line for the spine classifier.
func FormatIsolationHint(profile, workspaceRoot string) string {
	return fmt.Sprintf("%s profile=%s workspace_root=%s", tagIsolation, profile, workspaceRoot)
}
