package bridge

import (
	"agentgo/internal/workspace"
	"errors"
	"strings"
)

// SelectWorkspaceDirectory (workspace_dialog_desktop.go / workspace_dialog_web.go)
// only selects a path. The frontend still runs its unsaved-file guards and
// SetWorkspaceRoot through the normal switch flow.

func workspaceDirectorySelection(path string) map[string]any {
	if strings.TrimSpace(path) == "" {
		return map[string]any{"success": true, "cancelled": true, "path": ""}
	}
	root, err := workspace.NormalizeRoot(path)
	if err != nil {
		return errReply(err)
	}
	return map[string]any{"success": true, "cancelled": false, "path": root}
}

// Wails alpha.97 wraps its internal CFD cancellation sentinel, which cannot
// be imported outside Wails. Match that exact sentinel or a Windows cancel HRESULT.
func workspaceDialogCancelled(err error) bool {
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if cause.Error() == "cancelled by user" {
			return true
		}
		if code, ok := cause.(interface{ Code() uintptr }); ok && uint32(code.Code()) == 0x800704c7 {
			return true
		}
	}
	return false
}
