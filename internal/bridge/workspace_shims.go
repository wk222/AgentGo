package bridge

import (
	"agentgo/internal/workspace"
)

// Aliases onto internal/workspace so bridge call sites (and the Wails binding
// surface, e.g. WorkspaceInfo) keep their names while the logic lives in the
// Foundation package.
type (
	WorkspaceInfo         = workspace.Info
	WorkspaceReviewChange = workspace.ReviewChange
	workspaceReviewStore  = workspace.ReviewStore
)

var (
	runGit                   = workspace.RunGit
	hideExecWindow           = workspace.HideExecWindow
	cleanWorkspaceRel        = workspace.CleanRel
	workspaceFullPath        = workspace.FullPath
	shouldSkipWorkspaceEntry = workspace.ShouldSkipEntry
	workspaceGitStatuses     = workspace.GitStatuses
	gitStatusForEntry        = workspace.GitStatusForEntry
	normalizeWorkspaceRoot   = workspace.NormalizeRoot
	buildWorkspaceInfo       = workspace.BuildInfo
	samePath                 = workspace.SamePath
	newWorkspaceReviewStore  = workspace.NewReviewStore
)
