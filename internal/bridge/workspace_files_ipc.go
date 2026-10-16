package bridge

import (
	"errors"
	"strings"

	"agentgo/internal/workspace"
)

// WorkspaceEntry is the directory-listing row type (logic lives in internal/workspace).
type WorkspaceEntry = workspace.Entry

func errReply(err error) map[string]any {
	return map[string]any{"success": false, "error": err.Error()}
}

func pathReply(path string, err error) map[string]any {
	if err != nil {
		return errReply(err)
	}
	return map[string]any{"success": true, "path": path}
}

func (s *AppService) ListWorkspace(relPath string) []WorkspaceEntry {
	return workspace.List(s.rt.WorkspaceRoot(), relPath)
}

func (s *AppService) ListWorkspaceDir(relPath string) []WorkspaceEntry {
	return s.ListWorkspace(relPath)
}

func (s *AppService) GetWorkspaceInfo() map[string]any {
	info := s.rt.WorkspaceInfo()
	return map[string]any{
		"success":             true,
		"root":                info.Root,
		"name":                info.Name,
		"git_root":            info.GitRoot,
		"branch":              info.Branch,
		"dirty_count":         info.DirtyCount,
		"is_git":              info.IsGit,
		"writable":            info.Writable,
		"warning":             info.Warning,
		"preview_limit_bytes": info.PreviewLimitBytes,
		"watch_mode":          info.WatchMode,
	}
}

func (s *AppService) ReadWorkspaceFile(relPath string) map[string]any {
	f, err := workspace.ReadText(s.rt.WorkspaceRoot(), relPath)
	if err != nil {
		if errors.Is(err, workspace.ErrTooLarge) {
			return map[string]any{"success": false, "error": err.Error(), "path": f.Path, "size": f.Size}
		}
		return errReply(err)
	}
	return map[string]any{"success": true, "path": f.Path, "size": f.Size, "content": f.Content}
}

// WriteWorkspaceFile writes content to a file inside the workspace safely.
func (s *AppService) WriteWorkspaceFile(relPath string, content string) map[string]any {
	clean, err := workspace.WriteText(s.rt.WorkspaceRoot(), relPath, content)
	if err != nil {
		return errReply(err)
	}
	return map[string]any{"success": true, "path": clean, "size": len(content)}
}

// CreateWorkspaceFile creates a new file at relPath.
func (s *AppService) CreateWorkspaceFile(relPath string) map[string]any {
	return pathReply(workspace.CreateFile(s.rt.WorkspaceRoot(), relPath))
}

// CreateWorkspaceDir creates a new directory at relPath.
func (s *AppService) CreateWorkspaceDir(relPath string) map[string]any {
	return pathReply(workspace.CreateDir(s.rt.WorkspaceRoot(), relPath))
}

// DeleteWorkspacePath deletes a file or folder at relPath.
func (s *AppService) DeleteWorkspacePath(relPath string) map[string]any {
	return pathReply(workspace.Delete(s.rt.WorkspaceRoot(), relPath))
}

// RenameWorkspacePath renames a file or folder.
func (s *AppService) RenameWorkspacePath(oldPath, newPath string) map[string]any {
	return pathReply(workspace.Rename(s.rt.WorkspaceRoot(), oldPath, newPath))
}

// ExecuteTerminalCommand runs a command in the workspace and returns output.
func (s *AppService) ExecuteTerminalCommand(command string) map[string]any {
	if strings.TrimSpace(command) == "" {
		return map[string]any{"success": false, "error": "empty command"}
	}
	r := workspace.RunShell(s.rt.WorkspaceRoot(), command)
	return map[string]any{"success": r.Err == nil, "stdout": r.Stdout, "stderr": r.Stderr, "exit_code": r.ExitCode}
}
