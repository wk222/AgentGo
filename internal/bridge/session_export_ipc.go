package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentgo/internal/applog"
	"agentgo/internal/sessions"
)

const (
	// The prefix is specific on purpose: pruning must never touch a file the user put into attachments.
	sessionExportPrefix = "agentgo-chat-"
	sessionExportKeep   = 30
)

var sessionExportMu sync.Mutex // two clicks must not write the same file

// RenameSession gives a conversation the title the user typed. The title is cleaned (one line, bounded) and the
// cleaned form is returned, so the page shows what was stored.
func (s *AppService) RenameSession(sessionID, title string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return map[string]any{"success": false, "error": "empty session_id"}
	}
	clean, err := s.rt.Sessions().Rename(context.Background(), sessionID, title)
	if err != nil {
		applog.Session("RenameSession id=%s err=%v", sessionID, err)
		msg := err.Error()
		switch {
		case errors.Is(err, sessions.ErrNotFound):
			msg = "这条对话已经不存在"
		case strings.Contains(msg, "title is empty"):
			msg = "标题不能为空"
		}
		return map[string]any{"success": false, "error": msg}
	}
	applog.Session("RenameSession id=%s title=%q", sessionID, clean)
	return map[string]any{"success": true, "id": sessionID, "title": clean}
}

// ExportSession writes a conversation as a Markdown file next to the attachments, which both hosts already serve
// to the page: the answer carries the url to offer as a download, the name the downloaded file should have and the
// path for anyone who prefers the file manager.
func (s *AppService) ExportSession(sessionID string) map[string]any {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return map[string]any{"success": false, "error": "empty session_id"}
	}
	if s.rt == nil || s.rt.DataDir() == "" {
		return map[string]any{"success": false, "error": "数据目录尚未就绪"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store := s.rt.Sessions()
	sess, err := store.Get(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sessions.ErrNotFound) {
			return map[string]any{"success": false, "error": "这条对话已经不存在"}
		}
		return map[string]any{"success": false, "error": "读取对话失败：" + err.Error()}
	}
	msgs, err := store.GetMessages(ctx, sessionID, sessions.ExportMaxMessages)
	if err != nil {
		return map[string]any{"success": false, "error": "读取对话失败：" + err.Error()}
	}
	now := time.Now()
	text := sessions.Markdown(sess, msgs, now, sess.MessageCount > len(msgs))
	name := sessionExportName(sess.ID, now)
	path, size, err := writeSessionExport(filepath.Join(s.rt.DataDir(), "attachments"), name, text)
	if err != nil {
		applog.Session("ExportSession id=%s err=%v", sessionID, err)
		return map[string]any{"success": false, "error": "生成导出文件失败：" + err.Error()}
	}
	applog.Session("ExportSession id=%s messages=%d bytes=%d", sessionID, len(msgs), size)
	return map[string]any{
		"success":  true,
		"name":     name,
		"filename": sessions.SafeFileName(sess.Title, "对话") + "-" + now.Format("20060102") + ".md",
		"path":     path,
		"size":     size,
		"url":      "/attachments/" + name,
		"messages": len(msgs),
	}
}

// sessionExportName is the name the export is stored under: ASCII only, so it is a safe URL path whatever the
// title says, and led by the time so that names sort chronologically.
func sessionExportName(sessionID string, now time.Time) string {
	var id strings.Builder
	for _, r := range sessionID {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			id.WriteRune(r)
		}
	}
	return fmt.Sprintf("%s%s-%s.md", sessionExportPrefix, now.Format("20060102-150405"), id.String())
}

// writeSessionExport stores text as dir/name (through a temporary name, so a download never sees half a file) and
// keeps only the newest sessionExportKeep exports.
func writeSessionExport(dir, name, text string) (string, int64, error) {
	sessionExportMu.Lock()
	defer sessionExportMu.Unlock()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	final := filepath.Join(dir, name)
	tmp := final + ".tmp"
	// The byte order mark makes the notepad of Windows 7 (and WPS) read the file as UTF-8 instead of showing
	// Chinese text as garbage; every other reader ignores it.
	if err := os.WriteFile(tmp, append([]byte("\xEF\xBB\xBF"), text...), 0o644); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	pruneSessionExports(dir, sessionExportKeep)
	st, err := os.Stat(final)
	if err != nil {
		return "", 0, err
	}
	return final, st.Size(), nil
}

// pruneSessionExports removes all but the newest keep exports and any half-written file left by a crash.
func pruneSessionExports(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var exports []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, sessionExportPrefix) {
			continue
		}
		switch {
		case strings.HasSuffix(n, ".md.tmp"):
			_ = os.Remove(filepath.Join(dir, n))
		case strings.HasSuffix(n, ".md"):
			exports = append(exports, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(exports)))
	for i := keep; i < len(exports); i++ {
		_ = os.Remove(filepath.Join(dir, exports[i]))
	}
}
