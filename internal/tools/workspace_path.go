package tools

// ResolveWorkspaceFile maps a workspace-relative path to an absolute one inside
// root for a file that may not exist yet. Absolute paths, ".." escapes and
// symlinked ancestors that lead outside the workspace are rejected. Plugins that
// create files (document generation) use it as their path jail.
func ResolveWorkspaceFile(root, rel string) (string, error) {
	_, abs, err := resolveWorkspacePath(root, rel, false, false)
	return abs, err
}
