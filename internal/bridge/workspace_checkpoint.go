package bridge

import (
	"context"
	"fmt"
	"log"
	"strings"

	"agentgo/internal/config"
	"agentgo/internal/core"
	"agentgo/internal/engine"
	"agentgo/internal/workspace"
)

// Per-turn rollback: the workspace's files are filed under every user turn
// before it starts, and RollbackToTurn puts them back (see
// docs/SYSTEM_DESIGN_2026-10-30.md).

type turnCheckpointStore = workspace.CheckpointStore

func newTurnCheckpointStore() *turnCheckpointStore {
	return workspace.NewCheckpointStore(workspace.DefaultCheckpointLimits())
}

const (
	// Keys of the user message's meta and of the "started" event payload.
	metaCheckpoint       = "checkpoint"
	startedCheckpoint    = "checkpoint_id"
	startedCheckpointErr = "checkpoint_error"
	// noteKindRollback marks the conversation line written after a rollback.
	noteKindRollback = "workspace_rollback"

	// rollbackPreviewCap bounds how many file changes a preview carries to the UI.
	rollbackPreviewCap = 200
)

// beginTurnCheckpoint files the workspace under the turn that is about to start.
// It never blocks a turn: without a checkpoint the turn just cannot be rolled
// back, and the "started" event says why. The id travels two ways: in the user
// message's meta (survives a reload) and in the "started" event (reaches the UI
// at once).
func (rt *Runtime) beginTurnCheckpoint(ctx context.Context, req engine.RunRequest) (context.Context, engine.RunRequest) {
	if rt.turnCheckpoints == nil || !config.TurnCheckpoints.On() || strings.TrimSpace(req.SessionID) == "" {
		return ctx, req
	}
	root := rt.WorkspaceRoot()
	if root == "" || (strings.TrimSpace(req.Workspace) != "" && !samePath(req.Workspace, root)) {
		return ctx, req // a run in a folder other than the workspace is out of scope
	}
	extra := make(map[string]any, len(req.StartedExtra)+1)
	for k, v := range req.StartedExtra {
		extra[k] = v
	}
	req.StartedExtra = extra
	info, err := rt.turnCheckpoints.Begin(req.SessionID, root, req.Input)
	if err != nil {
		log.Printf("[checkpoint] session=%s: %v", req.SessionID, err)
		extra[startedCheckpointErr] = err.Error()
		return ctx, req
	}
	extra[startedCheckpoint] = info.ID
	return core.WithTurnMeta(ctx, map[string]any{metaCheckpoint: info.ID}), req
}

func checkpointsUnavailable() map[string]any {
	return map[string]any{"success": false, "error": "turn checkpoints are not available"}
}

// rollbackBusyReason is empty when files may be rolled back. Stricter than a
// workspace switch: runs of any engine (not only the built-in agent) share the
// folder, and all of them would be working on files that change under them.
func (rt *Runtime) rollbackBusyReason() string {
	if why := rt.workspaceBusyReason(); why != "" {
		return why
	}
	if rt.engineRouter != nil {
		if n := len(rt.engineRouter.Active()); n > 0 {
			return fmt.Sprintf("%d engine run(s) in progress; stop them first", n)
		}
	}
	return ""
}

// ListTurnCheckpoints returns the turns of a session that can still be rolled
// back, oldest first. Recycled checkpoints and those of another workspace are
// not listed, so the UI offers a rollback only where one can work.
func (s *AppService) ListTurnCheckpoints(sessionID string) map[string]any {
	rt := s.rt
	enabled := config.TurnCheckpoints.On()
	out := []workspace.CheckpointInfo{}
	if rt.turnCheckpoints != nil {
		root := rt.WorkspaceRoot()
		for _, c := range rt.turnCheckpoints.List(sessionID) {
			if c.Kind == workspace.CheckpointTurn && samePath(c.Root, root) {
				out = append(out, c)
			}
		}
	}
	return map[string]any{"success": true, "enabled": enabled, "checkpoints": out}
}

// PreviewTurnRollback says what rolling back to a checkpoint would do, without touching anything.
func (s *AppService) PreviewTurnRollback(sessionID, checkpointID string) map[string]any {
	rt := s.rt
	if rt.turnCheckpoints == nil {
		return checkpointsUnavailable()
	}
	p, err := rt.turnCheckpoints.Preview(sessionID, rt.WorkspaceRoot(), strings.TrimSpace(checkpointID))
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	more := 0
	if len(p.Changes) > rollbackPreviewCap {
		more = len(p.Changes) - rollbackPreviewCap
		p.Changes = p.Changes[:rollbackPreviewCap]
	}
	return map[string]any{"success": true, "preview": p, "more": more, "busy": rt.rollbackBusyReason()}
}

// RollbackToTurn puts the workspace's files back to how they were before a turn.
// Refused while any run is in progress or waiting for approval: they share the
// workspace and would be working on files that change under them.
func (s *AppService) RollbackToTurn(sessionID, checkpointID string) map[string]any {
	rt := s.rt
	if rt.turnCheckpoints == nil {
		return checkpointsUnavailable()
	}
	if why := rt.rollbackBusyReason(); why != "" {
		return map[string]any{"success": false, "busy": true, "error": why}
	}
	id := strings.TrimSpace(checkpointID)
	info, _ := rt.turnCheckpoints.Get(sessionID, id)
	res, err := rt.turnCheckpoints.Rollback(sessionID, rt.WorkspaceRoot(), id)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if res.Restored+res.Recreated+res.Deleted > 0 {
		// The model's memory still contains the edits that were just taken back.
		// Not under the "checkpoint" key: that one marks a user message that can be rolled back to.
		transcriptOf(rt).Note(sessionID, rollbackNote(info, res), map[string]any{
			"kind": noteKindRollback, "rollback_target": id, "undo_checkpoint": res.UndoID,
		})
	}
	out := map[string]any{"success": len(res.Failed) == 0, "result": res, "undo_checkpoint_id": res.UndoID}
	if n := len(res.Failed); n > 0 {
		out["error"] = fmt.Sprintf("%d file(s) could not be written back", n)
	}
	return out
}

// rollbackNote is the line the model reads on the next turn.
func rollbackNote(info workspace.CheckpointInfo, res *workspace.RollbackResult) string {
	var b strings.Builder
	b.WriteString("【系统记录】")
	switch {
	case info.Kind == workspace.CheckpointPreRollback:
		b.WriteString("用户撤销了上一次回退，工作区文件已恢复到那次回退之前的状态")
	case info.Prompt != "":
		fmt.Fprintf(&b, "用户把工作区文件回退到了「%s」这一轮开始之前的状态", info.Prompt)
	default:
		b.WriteString("用户把工作区文件回退到了之前某一轮开始时的状态")
	}
	fmt.Fprintf(&b, "（还原 %d 个、重建 %d 个、删除 %d 个文件", res.Restored, res.Recreated, res.Deleted)
	if n := len(res.Skipped); n > 0 {
		fmt.Fprintf(&b, "；另有 %d 个文件因体积过大等原因未回退", n)
	}
	b.WriteString("）。此后对话中提到的文件改动可能已经不存在，继续之前请先重新读取相关文件确认现状。")
	return b.String()
}
