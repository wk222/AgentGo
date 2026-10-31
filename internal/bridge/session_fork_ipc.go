package bridge

import (
	"context"
	"errors"
	"strings"
	"time"

	"agentgo/internal/applog"
	"agentgo/internal/sessions"
)

const (
	forkBusyRunning  = "这条对话正在运行。等它结束，或先点停止，再分叉。"
	forkBusyApproval = "这条对话停在等你批准或回答。先处理完它，再分叉。"
)

// ForkSession copies a whole conversation into a new one so it can be taken in
// another direction: the transcript, what the model has been told, the session
// mode and the plan task list. Afterwards the two are independent; they only
// share the workspace (files on disk) and the memory that is not tied to one
// conversation. The original is not changed.
//
// title may be empty, in which case the new conversation is called
// "<original>（分叉）". The answer carries the new session, so the page can switch
// to it without asking for the list first.
func (s *AppService) ForkSession(sessionID, title string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return map[string]any{"success": false, "error": "empty session_id"}
	}
	if s.rt == nil || s.rt.Sessions() == nil {
		return map[string]any{"success": false, "error": "会话存储尚未就绪"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if why := s.sessionForkBlocker(ctx, sessionID); why != "" {
		applog.Session("ForkSession id=%s refused: %s", sessionID, why)
		return map[string]any{"success": false, "error": why, "busy": true}
	}
	started := time.Now()
	res, err := s.rt.Sessions().ForkSession(ctx, sessionID, title)
	if err != nil {
		applog.Session("ForkSession id=%s err=%v", sessionID, err)
		msg := "分叉失败：" + err.Error()
		if errors.Is(err, sessions.ErrNotFound) {
			msg = "这条对话已经不存在"
		}
		return map[string]any{"success": false, "error": msg}
	}

	// The conversation is complete in the database by now; what the runner keeps
	// for itself is best effort, and said so when it fails.
	warnings := []string{}
	tasks := 0
	if runner := s.rt.AgentRunner(); runner != nil {
		adopted, err := runner.AdoptForkedSession(ctx, sessionID, res.Session.ID)
		tasks = adopted.Tasks
		if err != nil {
			applog.Session("ForkSession id=%s fork=%s adopt err=%v", sessionID, res.Session.ID, err)
			warnings = append(warnings, "对话已分叉，但计划任务清单没能一并复制："+err.Error())
		}
	}
	applog.Session("ForkSession id=%s fork=%s messages=%d events=%d tasks=%d mode=%v ms=%d",
		sessionID, res.Session.ID, res.Messages, res.Events, tasks, res.Mode, time.Since(started).Milliseconds())
	return map[string]any{
		"success":      true,
		"id":           res.Session.ID,
		"session":      res.Session,
		"parent_id":    res.ParentID,
		"parent_title": res.ParentTitle,
		"messages":     res.Messages,
		"events":       res.Events,
		"tasks":        tasks,
		"warnings":     warnings,
	}
}

// sessionForkBlocker says why a conversation cannot be forked right now ("" when
// it can). A run in flight keeps writing to the original while the copy is taken,
// and a run waiting for a decision has a tool call without its result, which the
// model would be handed on the first message to the copy.
func (s *AppService) sessionForkBlocker(ctx context.Context, sessionID string) string {
	if runner := s.rt.AgentRunner(); runner != nil && runner.IsSessionRunning(sessionID) {
		return forkBusyRunning
	}
	if s.rt.engineRouter != nil {
		for _, run := range s.rt.engineRouter.Active() {
			if run.SessionID == sessionID {
				return forkBusyRunning
			}
		}
	}
	for _, p := range s.rt.pending.Snapshot() {
		if p.SessionID == sessionID {
			return forkBusyApproval
		}
	}
	// Another process (desktop, terminal, headless) may own the pause.
	if rows, err := s.rt.Sessions().ListPendingRuns(ctx); err == nil {
		for _, p := range rows {
			if p.SessionID == sessionID {
				return forkBusyApproval
			}
		}
	}
	return ""
}
