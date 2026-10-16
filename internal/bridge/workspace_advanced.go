package bridge

import (
	"strings"

	"agentgo/internal/workspace"
)

// IPC wrappers over internal/workspace (search / git / media). The logic lives
// in the workspace package; this file only maps results to the UI's JSON shape.

func gitReply(out string, err error) map[string]any {
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "output": out}
	}
	return map[string]any{"success": true, "output": out}
}

// WorkspaceSearch performs full-text or regex search across workspace files.
func (s *AppService) WorkspaceSearch(query string, isRegex, matchCase, matchWholeWord bool, includePattern, excludePattern string, maxResults int) map[string]any {
	results, total, err := workspace.Search(s.rt.WorkspaceRoot(), workspace.SearchOptions{
		Query: query, IsRegex: isRegex, MatchCase: matchCase, WholeWord: matchWholeWord,
		Include: includePattern, Exclude: excludePattern, MaxResults: maxResults,
	})
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "results": results, "totalMatches": total, "totalFiles": len(results)}
}

// WorkspaceGitStatus gets full status of Git working tree.
func (s *AppService) WorkspaceGitStatus() map[string]any {
	st, err := workspace.GitStatus(s.rt.WorkspaceRoot())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{
		"success": true, "branch": st.Branch,
		"staged": st.Staged, "unstaged": st.Unstaged, "untracked": st.Untracked, "total": st.Total(),
	}
}

// WorkspaceGitStage stages a file or all files.
func (s *AppService) WorkspaceGitStage(relPath string) map[string]any {
	return gitReply(workspace.GitStage(s.rt.WorkspaceRoot(), relPath))
}

// WorkspaceGitUnstage unstages a file or all files.
func (s *AppService) WorkspaceGitUnstage(relPath string) map[string]any {
	return gitReply(workspace.GitUnstage(s.rt.WorkspaceRoot(), relPath))
}

// WorkspaceGitCommit creates a commit.
func (s *AppService) WorkspaceGitCommit(message string) map[string]any {
	if strings.TrimSpace(message) == "" {
		return map[string]any{"success": false, "error": "commit message is empty"}
	}
	return gitReply(workspace.GitCommit(s.rt.WorkspaceRoot(), message))
}

// WorkspaceGitBranches lists branches.
func (s *AppService) WorkspaceGitBranches() map[string]any {
	branches, current, err := workspace.GitBranches(s.rt.WorkspaceRoot())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "current": current, "branches": branches}
}

// WorkspaceGitCheckout switches branch.
func (s *AppService) WorkspaceGitCheckout(branch string) map[string]any {
	if strings.TrimSpace(branch) == "" {
		return map[string]any{"success": false, "error": "empty branch name"}
	}
	return gitReply(workspace.GitCheckout(s.rt.WorkspaceRoot(), branch))
}

// WorkspaceReadBase64 reads a binary or media file as base64 data.
func (s *AppService) WorkspaceReadBase64(relPath string) map[string]any {
	m, err := workspace.ReadMedia(s.rt.WorkspaceRoot(), relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "path": m.Path, "mime": m.Mime, "size": m.Size, "base64": m.Base64, "data_url": m.DataURL}
}

// WorkspaceDiagnostics retrieves active LSP diagnostics across the workspace.
func (s *AppService) WorkspaceDiagnostics() map[string]any {
	tb := s.getCodetools()
	if tb == nil {
		return map[string]any{"success": false, "total": 0, "servers": map[string]any{}}
	}
	states := tb.LSPStates()
	servers := make(map[string]any, len(states))
	total := 0
	for name, st := range states {
		total += st.DiagnosticCount
		servers[name] = map[string]any{
			"state":            st.State,
			"diagnostic_count": st.DiagnosticCount,
			"diagnostics":      tb.LSPDiagnostics(name),
		}
	}
	return map[string]any{"success": true, "total": total, "servers": servers}
}
