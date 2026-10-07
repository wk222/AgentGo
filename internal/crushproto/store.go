package crushproto

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot is everything the server remembers about one project directory, so
// a TUI restart (which deletes and re-creates the workspace) finds its sessions.
type Snapshot struct {
	Sessions []Session             `json:"sessions"`
	Messages map[string][]*Message `json:"messages"`
	Files    map[string][]File     `json:"files"`
	// Links maps a session id to an external id (AgentGo's own session).
	Links map[string]string `json:"links,omitempty"`
}

// Store persists Snapshots keyed by project path.
type Store interface {
	Load(path string) (*Snapshot, error) // (nil, nil) when nothing is stored
	Save(path string, s *Snapshot) error
}

// FileStore keeps one JSON file per project path under Dir.
type FileStore struct{ Dir string }

// pathKey identifies a project directory (Windows paths are case-insensitive).
func pathKey(path string) string { return strings.ToLower(filepath.Clean(path)) }

func (f FileStore) file(path string) string {
	sum := sha256.Sum256([]byte(pathKey(path)))
	return filepath.Join(f.Dir, hex.EncodeToString(sum[:8])+".json")
}

func (f FileStore) Load(path string) (*Snapshot, error) {
	b, err := os.ReadFile(f.file(path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Save writes atomically (temp file + rename) so a crash never leaves a torn file.
func (f FileStore) Save(path string, s *Snapshot) error {
	if err := os.MkdirAll(f.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	dst := f.file(path)
	tmp, err := os.CreateTemp(f.Dir, "snap-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

const persistDelay = 400 * time.Millisecond

// dirtyLocked schedules a debounced save. Caller holds w.mu.
func (w *workspace) dirtyLocked() {
	if w.store == nil {
		return
	}
	if w.persistTimer == nil {
		w.persistTimer = time.AfterFunc(persistDelay, w.flush)
		return
	}
	w.persistTimer.Reset(persistDelay)
}

// flush writes the current state now. Saves are serialized so an older
// snapshot can never overwrite a newer one.
func (w *workspace) flush() {
	if w.store == nil {
		return
	}
	w.saveMu.Lock()
	defer w.saveMu.Unlock()
	w.mu.Lock()
	snap := &Snapshot{
		Sessions: make([]Session, 0, len(w.sessions)),
		Messages: make(map[string][]*Message, len(w.msgs)),
		Files:    make(map[string][]File, len(w.files)),
		Links:    make(map[string]string, len(w.links)),
	}
	for _, s := range w.sessions {
		snap.Sessions = append(snap.Sessions, *s)
	}
	for sid, list := range w.msgs {
		cp := make([]*Message, len(list))
		for i, m := range list {
			cp[i] = cloneMessage(m)
		}
		snap.Messages[sid] = cp
	}
	for sid, fs := range w.files {
		snap.Files[sid] = append([]File(nil), fs...)
	}
	for k, v := range w.links {
		snap.Links[k] = v
	}
	w.mu.Unlock()
	if err := w.store.Save(w.path, snap); err != nil && w.logf != nil {
		w.logf("persist %s: %v", w.path, err)
	}
}

// restore loads the stored state for this project path, if any.
func (w *workspace) restore() {
	if w.store == nil {
		return
	}
	snap, err := w.store.Load(w.path)
	if err != nil {
		if w.logf != nil {
			w.logf("restore %s: %v", w.path, err)
		}
		return
	}
	if snap == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range snap.Sessions {
		s := snap.Sessions[i]
		s.IsBusy = false
		w.sessions = append(w.sessions, &s)
	}
	for sid, list := range snap.Messages {
		for _, m := range list {
			closeUnfinished(m)
		}
		w.msgs[sid] = list
	}
	for sid, fs := range snap.Files {
		if w.files == nil {
			w.files = map[string][]File{}
		}
		w.files[sid] = fs
	}
	for k, v := range snap.Links {
		w.links[k] = v
	}
}

// closeUnfinished marks an assistant message that never got its finish part
// (the previous process died mid-run) as cancelled, so a client does not show
// it as still running.
func closeUnfinished(m *Message) {
	if m.Role != "assistant" {
		return
	}
	for _, p := range m.Parts {
		if p.Type == "finish" {
			return
		}
	}
	m.Parts = append(m.Parts, Part{Type: "finish", Data: map[string]any{"reason": "canceled", "time": 0}})
}

// Link returns the external id (AgentGo session) tied to this run's session.
func (e *Emitter) Link() (string, bool) {
	e.ws.mu.Lock()
	defer e.ws.mu.Unlock()
	id, ok := e.ws.links[e.sessionID]
	return id, ok
}

// SetLink ties this run's session to an external id; it survives restarts.
func (e *Emitter) SetLink(id string) {
	e.ws.mu.Lock()
	e.ws.links[e.sessionID] = id
	e.ws.dirtyLocked()
	e.ws.mu.Unlock()
}
