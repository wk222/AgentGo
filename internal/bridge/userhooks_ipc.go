package bridge

import (
	"path/filepath"

	"agentgo/internal/userhooks"
)

// The settings page shows the user's hooks: where the file is, whether it could
// be read, what it holds and what ran lately. The file itself is only ever
// edited by the user; nothing here writes it.

// hooksStatus describes the hooks, looking at the file again first when reload is set.
func (rt *Runtime) hooksStatus(reload bool) userhooks.Status {
	e := rt.hooksEngine()
	var st userhooks.Status
	if reload {
		st = e.Reload()
	} else {
		st = e.Status()
	}
	if e == nil { // switched off or stopped: say where the file would be and why nothing runs
		st.Path = filepath.Join(rt.dataDir, userHooksFile)
		st.DisabledReason = "钩子功能已关闭：环境变量 AGENTGO_USER_HOOKS=0，或 userhooks 插件已停用"
	}
	return st
}

// GetHooksStatus returns the state of the hooks.json hooks: the file's path and
// parse result, the hooks it configures per event, and the most recent runs
// (newest first, at most 200, kept in memory only).
func (s *AppService) GetHooksStatus() userhooks.Status { return s.rt.hooksStatus(false) }

// ReloadHooks reads hooks.json again right away instead of waiting for the next
// check, and returns the new state. A file that does not parse changes nothing:
// the previous hooks stay in use and the error is in the result.
func (s *AppService) ReloadHooks() userhooks.Status { return s.rt.hooksStatus(true) }
