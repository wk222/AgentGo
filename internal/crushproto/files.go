package crushproto

import "net/http"

// File is one stored version of a file touched during a session. The TUI's
// "Modified Files" sidebar diffs the lowest version (the content BEFORE the
// session touched it) against the highest.
type File struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Version   int64  `json:"version"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// recordFile stores before/after of a changed file and publishes file/created
// for each new version. The first change to a path also stores the "before"
// content as version 0.
func (w *workspace) recordFile(sessionID, path, before, after string) {
	now := nowSec()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.files == nil {
		w.files = map[string][]File{}
	}
	var next int64
	seen := false
	for _, f := range w.files[sessionID] {
		if f.Path == path {
			seen = true
			if f.Version >= next {
				next = f.Version + 1
			}
		}
	}
	add := func(content string) {
		f := File{ID: newID(), SessionID: sessionID, Path: path, Content: content, Version: next, CreatedAt: now, UpdatedAt: now}
		next++
		w.files[sessionID] = append(w.files[sessionID], f)
		if frame, err := envelope("file", "created", f); err == nil {
			w.publishLocked(frame)
		}
	}
	if !seen {
		add(before)
	}
	add(after)
	w.dirtyLocked()
}

func (w *workspace) sessionFiles(sessionID string) []File {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]File{}, w.files[sessionID]...)
}

func (s *Server) listHistory(w http.ResponseWriter, r *http.Request) {
	if ws := s.workspace(w, r); ws != nil {
		writeJSON(w, 200, ws.sessionFiles(r.PathValue("sid")))
	}
}

// FileChanged reports that the agent changed path from before to after, so the
// client can show it under "Modified Files".
func (e *Emitter) FileChanged(path, before, after string) {
	if before == after {
		return
	}
	e.ws.recordFile(e.sessionID, path, before, after)
}
