package bridge

import (
	"context"
	"path/filepath"
	"time"

	"agentgo/internal/applog"
)

// PingIPC is a no-op IPC probe (should log [ipc] PingIPC immediately).
func (s *AppService) PingIPC() map[string]any {
	applog.IPC("PingIPC", "ok")
	return map[string]any{"ok": true, "ts": time.Now().Unix()}
}

// GetAgentRunStatus returns backend phase/chunk/trace for UI (where a run is stuck).
func (s *AppService) GetAgentRunStatus(sessionID string) map[string]any {
	if s.rt == nil || s.rt.runTrack == nil {
		return map[string]any{"phase": "idle", "hint": "run tracker unavailable"}
	}
	return s.rt.runTrack.Snapshot(sessionID)
}

// GetDiagnostics returns paths and session DB stats for troubleshooting (Wails IPC).
func (s *AppService) GetDiagnostics() map[string]any {
	ctx := context.Background()
	out := map[string]any{
		"log_file": applog.Path(),
	}
	if s.rt != nil {
		out["data_dir"] = s.rt.DataDir()
		out["db_path"] = filepath.Join(s.rt.DataDir(), "agentgo.db")
	}
	list, err := s.rt.Sessions().List(ctx, 200)
	if err != nil {
		out["sessions_error"] = err.Error()
		return out
	}
	out["session_count"] = len(list)
	ids := make([]string, 0, len(list))
	for _, x := range list {
		ids = append(ids, x.ID)
	}
	out["session_ids"] = ids
	return out
}
