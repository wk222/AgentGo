//go:build !web

package bridge

// SelectWorkspaceDirectory only selects a path. The frontend still runs its
// unsaved-file guards and SetWorkspaceRoot through the normal switch flow.
func (s *AppService) SelectWorkspaceDirectory() map[string]any {
	if s.app == nil {
		return map[string]any{"success": false, "error": "文件夹选择器需要在 AgentGo 桌面应用中使用"}
	}
	dialog := s.app.Dialog.OpenFile().SetTitle("选择工作区文件夹").CanChooseFiles(false).CanChooseDirectories(true).CanCreateDirectories(true)
	if s.rt != nil {
		if root := s.rt.WorkspaceRoot(); root != "" {
			dialog.SetDirectory(root)
		}
	}
	if window := s.app.Window.Current(); window != nil {
		dialog.AttachToWindow(window)
	}
	path, err := dialog.PromptForSingleSelection()
	if workspaceDialogCancelled(err) {
		return workspaceDirectorySelection("")
	}
	if err != nil {
		return errReply(err)
	}
	return workspaceDirectorySelection(path)
}
