package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"agentgo/internal/agent"
	"agentgo/internal/applog"
	"agentgo/internal/channels"
	"agentgo/internal/memory"
	"agentgo/internal/tools"
)

// --- Sessions ---

func (s *AppService) ListSessions() []map[string]any {
	ctx := context.Background()
	list, err := s.rt.Sessions().List(ctx, 100)
	if err != nil {
		applog.Session("ListSessions err=%v", err)
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, x := range list {
		out = append(out, map[string]any{
			"id": x.ID, "title": x.Title, "updated_at": x.UpdatedAt,
			"message_count": x.MessageCount,
		})
	}
	applog.Session("ListSessions count=%d", len(out))
	return out
}

func (s *AppService) NewSession(title string) map[string]any {
	ctx := context.Background()
	sess, err := s.rt.Sessions().Create(ctx, title)
	if err != nil {
		applog.Session("NewSession err=%v", err)
		return map[string]any{"success": false, "error": err.Error()}
	}
	applog.Session("NewSession id=%s title=%q", sess.ID, sess.Title)
	return map[string]any{
		"success": true,
		"id":      sess.ID,
		"session": sess,
	}
}

// DeleteSession removes a chat session and its messages from SQLite.
func (s *AppService) DeleteSession(sessionID string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return map[string]any{"success": false, "error": "empty session_id"}
	}
	ctx := context.Background()
	if err := s.rt.Sessions().Delete(ctx, sessionID); err != nil {
		applog.Session("DeleteSession id=%s err=%v", sessionID, err)
		return map[string]any{"success": false, "error": err.Error()}
	}
	applog.Session("DeleteSession id=%s", sessionID)
	return map[string]any{"success": true, "id": sessionID}
}

// GetSessionMessages returns {session_id, messages, count} so the UI can verify IPC echoed the requested id.
func (s *AppService) GetSessionMessages(sessionID string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		applog.Warn("GetSessionMessages called with empty session_id")
		return map[string]any{"session_id": "", "messages": []map[string]any{}, "count": 0}
	}
	ctx := context.Background()
	msgs, err := s.rt.Sessions().GetMessages(ctx, sessionID, 200)
	if err != nil {
		applog.Session("GetSessionMessages id=%s err=%v", sessionID, err)
		return map[string]any{"session_id": sessionID, "messages": []map[string]any{}, "count": 0, "error": err.Error()}
	}
	out := make([]map[string]any, 0, len(msgs))
	skipped := 0
	bySid := map[string]int{}
	byType := map[string]int{}
	for _, m := range msgs {
		if m.SessionID != "" {
			bySid[m.SessionID]++
		}
		if m.SessionID != "" && m.SessionID != sessionID {
			skipped++
			continue
		}
		msgType := m.Type
		if msgType == "" {
			msgType = "text"
		}
		byType[msgType]++
		row := map[string]any{
			"role": m.Role, "type": msgType, "content": m.Content,
			"session_id": m.SessionID,
		}
		if m.MetaJSON != "" {
			_ = json.Unmarshal([]byte(m.MetaJSON), &row)
			row["type"] = msgType
			row["session_id"] = m.SessionID
		}
		// Transcript metadata describes the original pause. Decisions belong to
		// the approval queue and must be projected afresh after a UI reload.
		if msgType == "approval" && s.rt.Approvals() != nil {
			if id, _ := row["approval_id"].(string); id != "" {
				if req, err := s.rt.Approvals().GetRequest(ctx, id); err == nil && req != nil {
					row["status"] = req.Status
					row["resolved"] = req.Status != "pending"
				}
			}
		}
		if msgType == "aui" {
			applog.A2UI("db row want=%s component=%v data_json_len=%d",
				sessionID, row["component"], len(fmt.Sprint(row["data_json"])))
		}
		out = append(out, row)
	}
	if len(out) > 0 || skipped > 0 {
		applog.Session("GetSessionMessages want=%s db_total=%d returned=%d skipped_wrong_sid=%d types=%s sid_counts=%s",
			sessionID, len(msgs), len(out), skipped, applog.FormatCounts(byType), applog.FormatCounts(bySid))
	} else {
		applog.Session("GetSessionMessages want=%s empty (db_total=%d skipped=%d)", sessionID, len(msgs), skipped)
	}
	return map[string]any{
		"session_id": sessionID,
		"messages":   out,
		"count":      len(out),
	}
}

// --- Workspace ---

// sanitizeWorkspaceRel cleans a UI-supplied relative path and rejects escapes.
func sanitizeWorkspaceRel(relPath string) (string, error) {
	return cleanWorkspaceRel(relPath, false)
}

// WorkspaceFileDiff computes a git diff and also returns the two text snapshots
// required by Monaco's DiffEditor. Deleted files use an empty modified snapshot;
// untracked files use an empty original snapshot.
func (s *AppService) WorkspaceFileDiff(relPath string) map[string]any {
	root := s.rt.WorkspaceRoot()
	clean, err := sanitizeWorkspaceRel(relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}

	_, full, err := workspaceFullPath(root, clean, false)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	modifiedBytes, readErr := os.ReadFile(full)
	if readErr != nil && !os.IsNotExist(readErr) {
		return map[string]any{"success": false, "error": readErr.Error()}
	}
	modifiedContent := string(modifiedBytes)

	diffOut, err := runGit(root, "diff", "--no-color", "HEAD", "--", clean)
	if err != nil {
		// Repo may have no commits yet, or git/repo unavailable — surface it.
		applog.Warn("WorkspaceFileDiff path=%s err=%v", clean, err)
		return map[string]any{"success": false, "error": err.Error()}
	}

	untracked := false
	if strings.TrimSpace(diffOut) == "" {
		if st, e := runGit(root, "status", "--porcelain", "--", clean); e == nil {
			untracked = strings.HasPrefix(strings.TrimSpace(st), "??")
		}
	}

	originalContent := ""
	if !untracked {
		if headContent, showErr := runGit(root, "show", "HEAD:"+clean); showErr == nil {
			originalContent = headContent
		}
	}

	added, removed := 0, 0
	for _, ln := range strings.Split(diffOut, "\n") {
		switch {
		case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"):
			// file headers — ignore
		case strings.HasPrefix(ln, "+"):
			added++
		case strings.HasPrefix(ln, "-"):
			removed++
		}
	}

	return map[string]any{
		"success":          true,
		"path":             clean,
		"diff":             diffOut,
		"added":            added,
		"removed":          removed,
		"untracked":        untracked,
		"has_changes":      strings.TrimSpace(diffOut) != "" || untracked,
		"original_content": originalContent,
		"modified_content": modifiedContent,
	}
}

func (s *AppService) SetWorkspaceRoot(path string) map[string]any {
	info, err := s.rt.SetWorkspaceRoot(path)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{
		"success":             true,
		"root":                info.Root,
		"name":                info.Name,
		"git_root":            info.GitRoot,
		"branch":              info.Branch,
		"dirty_count":         info.DirtyCount,
		"is_git":              info.IsGit,
		"writable":            info.Writable,
		"warning":             info.Warning,
		"preview_limit_bytes": info.PreviewLimitBytes,
		"watch_mode":          info.WatchMode,
	}
}

// --- Memory pipeline ---

func (s *AppService) ListMemories(limit int) []memory.Record {
	ctx := context.Background()
	rows, _ := s.rt.memStore.ListActive(ctx, limit)
	return rows
}

func (s *AppService) MemoryFeedback(id string, signal string) map[string]any {
	ctx := context.Background()
	kind := memory.FeedbackKind(signal)
	if err := s.rt.Memory().Feedback(ctx, id, kind); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true}
}

func (s *AppService) MemoryGC() map[string]any {
	ctx := context.Background()
	p := s.rt.memoryPipeline()
	if p == nil {
		return map[string]any{"success": false, "error": "pipeline unavailable"}
	}
	n, err := p.RunGC(ctx, 30, 0.4)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "archived": n}
}

func (s *AppService) MemoryDistill(scope string) map[string]any {
	ctx := context.Background()
	p := s.rt.memoryPipeline()
	if p == nil {
		return map[string]any{"success": false, "error": "pipeline unavailable"}
	}
	if scope == "" {
		scope = "session"
	}
	summary, err := p.Distill(ctx, scope, 20)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "summary": summary}
}

func (s *AppService) MemoryContextPrompt(scope string) map[string]any {
	ctx := context.Background()
	if s.rt.Memory() == nil {
		return map[string]any{"success": false, "error": "memory unavailable"}
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	prompt, err := s.rt.Memory().ContextPrompt(ctx, scope)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "scope": scope, "prompt": prompt}
}

// --- Capability / Skills / Channels / Gateway (read-only status views) ---

func (s *AppService) ListCapabilities(kind string) []map[string]any {
	grants := s.rt.CapabilityBus().List(kind)
	out := make([]map[string]any, 0, len(grants))
	for _, g := range grants {
		out = append(out, map[string]any{
			"id": g.ID, "kind": g.Kind, "name": g.Name, "scope": g.Scope, "metadata": g.Metadata,
		})
	}
	return out
}

func (s *AppService) ListChannels() []channels.ChannelStatus {
	return channels.DefaultStatuses()
}

func (s *AppService) GetGatewayStatus() map[string]any {
	return s.sseGatewayStatus()
}

func (s *AppService) GetMiddlewareStack() []string {
	return agent.MiddlewareNames()
}

func (s *AppService) ListRegisteredTools() []string {
	ctx := context.Background()
	if s.rt.AgentRunner() == nil {
		return nil
	}
	return s.rt.AgentRunner().ToolNames(ctx)
}

// SendMessageWithSession attaches session persistence + ReAct when possible.
func (s *AppService) SendMessageWithSession(sessionID, userText string, images []string) SendMessageResult {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		applog.Warn("SendMessageWithSession empty session_id (desktop fallback)")
		return s.SendMessage(userText)
	}
	applog.IPC("SendMessageWithSession", "enter session=%s len=%d", sessionID, len(userText))
	ctx := s.transcript().Begin(context.Background(), sessionID, userText, images)
	res := s.sendMessageCore(ctx, sessionID, userText, images, nil)
	s.transcript().Finish(sessionID, coreMessages(res.Messages))
	return res
}

// SteerSession provides mid-flight guidance/instruction to a running agent task (Codex parity).
func (s *AppService) SteerSession(sessionID, instruction string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	instruction = strings.TrimSpace(instruction)
	if sessionID == "" || instruction == "" {
		return map[string]any{"success": false, "error": "session_id and instruction required"}
	}
	runner := s.rt.AgentRunner()
	if runner == nil {
		return map[string]any{"success": false, "error": "agent runner unavailable"}
	}
	if runner.SteerSessionRun(sessionID, instruction) {
		applog.IPC("SteerSession", "queued steer for session=%s: %q", sessionID, instruction)
		if s.app != nil {
			s.app.Event.Emit("chat:steered", map[string]any{
				"session_id":  sessionID,
				"instruction": instruction,
			})
		}
		return map[string]any{"success": true, "steered": true}
	}
	return map[string]any{"success": false, "steered": false, "reason": "session not actively running"}
}

// CancelA2UIInteraction unblocks a render_ui tool waiting on interact_id (stop button / new session).
func (s *AppService) CancelA2UIInteraction(interactID string) map[string]any {
	interactID = strings.TrimSpace(interactID)
	if interactID == "" {
		return map[string]any{"success": false, "error": "interact_id required"}
	}
	if s.rt.A2UIStore() == nil {
		return map[string]any{"success": false, "error": "A2UI store unavailable"}
	}
	s.rt.A2UIStore().Cancel(interactID)
	return map[string]any{"success": true}
}

func (s *AppService) ResolveA2UIInteraction(interactID string, action string, dataJSON string) map[string]any {
	if s.rt.A2UIStore() == nil {
		return map[string]any{"success": false, "error": "A2UI store unavailable"}
	}
	var data []byte
	if dataJSON != "" {
		data = []byte(dataJSON)
	}
	res := tools.InteractionResult{
		InteractID: interactID,
		Action:     action,
		Data:       data,
	}
	if err := s.rt.A2UIStore().Resolve(interactID, res); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true}
}
