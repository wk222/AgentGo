package bridge

import (
	"context"
	"errors"
	"strings"
	"time"

	"agentgo/internal/applog"
	"agentgo/internal/sessions"
)

// IPC for experts: the library, and which expert a conversation runs as. The
// page lists experts to choose from, opens a conversation as one, and shows
// which one a conversation already has.

const expertIPCTimeout = 10 * time.Second

func noSessionStore() map[string]any {
	return map[string]any{"success": false, "error": "会话存储尚未就绪"}
}

// ListExperts returns the experts a conversation can run as, grouped by the page.
// Rules are left out (GetExpert has them). warnings names the expert files that
// could not be used and why; user_dir and workspace_dir are the folders to put
// one's own in.
func (s *AppService) ListExperts() map[string]any {
	cat := s.rt.expertLibrary()
	rows := make([]map[string]any, 0, len(cat.Experts))
	for _, e := range cat.Experts {
		rows = append(rows, s.rt.expertRow(e, false))
	}
	warnings := cat.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	opts := s.rt.expertOptions()
	return map[string]any{
		"success": true, "experts": rows, "warnings": warnings,
		"user_dir": opts.UserDir, "workspace_dir": opts.WorkspaceDir,
	}
}

// GetExpert returns one expert with its rules. key is an id or a name.
func (s *AppService) GetExpert(key string) map[string]any {
	e, err := s.rt.expertLibrary().Find(key)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "expert": s.rt.expertRow(e, true)}
}

// GetSessionExpert says which expert a conversation runs as. missing is true when
// it is bound to one whose file is gone: it then runs without an expert.
func (s *AppService) GetSessionExpert(sessionID string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	st := s.rt.Sessions()
	if st == nil {
		return noSessionStore()
	}
	ctx, cancel := context.WithTimeout(context.Background(), expertIPCTimeout)
	defer cancel()
	id, err := st.Expert(ctx, sessionID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	out := map[string]any{"success": true, "session_id": sessionID, "expert_id": id, "expert": nil, "missing": false}
	if id == "" {
		return out
	}
	if e, ok := s.rt.expertLibrary().Get(id); ok {
		out["expert"] = s.rt.expertRow(e, false)
	} else {
		out["missing"] = true
	}
	return out
}

// SetSessionExpert makes a conversation run as an expert from its next message
// on (an id or a name; empty clears it). What was said before stays as it was.
func (s *AppService) SetSessionExpert(sessionID, expertKey string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return map[string]any{"success": false, "error": "empty session_id"}
	}
	st := s.rt.Sessions()
	if st == nil {
		return noSessionStore()
	}
	ctx, cancel := context.WithTimeout(context.Background(), expertIPCTimeout)
	defer cancel()
	if _, err := st.Get(ctx, sessionID); err != nil {
		if errors.Is(err, sessions.ErrNotFound) {
			return map[string]any{"success": false, "error": "这条对话已经不存在"}
		}
		return map[string]any{"success": false, "error": err.Error()}
	}
	out := map[string]any{"success": true, "session_id": sessionID, "expert_id": "", "expert": nil}
	bind := ""
	if key := strings.TrimSpace(expertKey); key != "" {
		e, err := s.rt.expertLibrary().Find(key)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		bind = e.ID
		out["expert_id"], out["expert"] = e.ID, s.rt.expertRow(e, false)
	}
	if err := st.SetExpert(ctx, sessionID, bind); err != nil {
		applog.Session("SetSessionExpert id=%s err=%v", sessionID, err)
		return map[string]any{"success": false, "error": err.Error()}
	}
	applog.Session("SetSessionExpert id=%s expert=%q", sessionID, bind)
	return out
}

// NewSessionWithExpert opens a conversation that runs as an expert from its
// first message. The answer has the same shape as NewSession, plus the expert.
func (s *AppService) NewSessionWithExpert(title, expertKey string) map[string]any {
	st := s.rt.Sessions()
	if st == nil {
		return noSessionStore()
	}
	e, err := s.rt.expertLibrary().Find(expertKey)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if strings.TrimSpace(title) == "" {
		title = "新对话"
	}
	ctx, cancel := context.WithTimeout(context.Background(), expertIPCTimeout)
	defer cancel()
	sess, err := st.Create(ctx, title)
	if err != nil {
		applog.Session("NewSessionWithExpert err=%v", err)
		return map[string]any{"success": false, "error": err.Error()}
	}
	if err := st.SetExpert(ctx, sess.ID, e.ID); err != nil {
		_ = st.Delete(ctx, sess.ID) // no half-made conversation
		applog.Session("NewSessionWithExpert id=%s bind err=%v", sess.ID, err)
		return map[string]any{"success": false, "error": err.Error()}
	}
	sess.ExpertID = e.ID
	applog.Session("NewSessionWithExpert id=%s title=%q expert=%s", sess.ID, sess.Title, e.ID)
	return map[string]any{"success": true, "id": sess.ID, "session": sess, "expert": s.rt.expertRow(e, false)}
}
