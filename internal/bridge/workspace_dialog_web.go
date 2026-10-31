//go:build web

package bridge

import (
	"context"

	"agentgo/internal/hostui"
)

// SelectWorkspaceDirectory only selects a path. The browser build asks the OS
// for a native folder dialog on the machine that runs AgentGo.
func (s *AppService) SelectWorkspaceDirectory() map[string]any {
	start := ""
	if s.rt != nil {
		start = s.rt.WorkspaceRoot()
	}
	path, err := hostui.PickFolder(context.Background(), "选择工作区文件夹", start)
	if err != nil {
		return errReply(err)
	}
	return workspaceDirectorySelection(path)
}
