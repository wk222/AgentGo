package acpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// A named session is worth keeping after its agent process is gone (the idle timeout, the cap on
// live agents, the app closing): the agent has its own record of the conversation and, if it can
// load sessions, a later call with the same label can carry on from it. This file is that
// bookkeeping: label -> which agent, which directory, which agent-side session id.

const (
	storeVersion = 1
	maxSaved     = 50                  // the newest this many are kept
	savedTTL     = 30 * 24 * time.Hour // an agent forgets old sessions too
	maxLabelLen  = 80
)

// savedSession is one label's record.
type savedSession struct {
	Label      string    `json:"label"`
	Agent      string    `json:"agent"`
	Cwd        string    `json:"cwd"`
	SessionID  string    `json:"session_id"`
	Permission string    `json:"permission,omitempty"` // what it last ran with; informational
	Updated    time.Time `json:"updated"`
}

type storeFile struct {
	Version  int            `json:"version"`
	Sessions []savedSession `json:"sessions"`
}

// sessionStore is a small JSON file. It is read and written whole each time: a few dozen records,
// touched once per finished turn.
type sessionStore struct {
	mu   sync.Mutex
	path string
	now  func() time.Time
}

func newSessionStore(path string) *sessionStore {
	return &sessionStore{path: path, now: time.Now}
}

// read returns the records that are still fresh, newest first. A missing or unreadable file is an
// empty store: losing the bookkeeping only costs a fresh session, and the next write replaces it.
func (s *sessionStore) read() []savedSession {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var f storeFile
	if json.Unmarshal(data, &f) != nil || f.Version != storeVersion {
		return nil
	}
	cutoff := s.now().Add(-savedTTL)
	out := f.Sessions[:0:0]
	for _, r := range f.Sessions {
		if r.Label != "" && r.SessionID != "" && r.Updated.After(cutoff) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out
}

// write replaces the file atomically, so a crash never leaves half a record.
func (s *sessionStore) write(list []savedSession) error {
	if len(list) > maxSaved {
		list = list[:maxSaved]
	}
	data, err := json.MarshalIndent(storeFile{Version: storeVersion, Sessions: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// get returns the record for a label.
func (s *sessionStore) get(label string) (savedSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.read() {
		if r.Label == label {
			return r, true
		}
	}
	return savedSession{}, false
}

// list returns every fresh record, newest first.
func (s *sessionStore) list() []savedSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

// put adds or replaces the record for r.Label.
func (s *sessionStore) put(r savedSession) error {
	if r.Label == "" || r.SessionID == "" {
		return errors.New("a saved session needs a label and a session id")
	}
	if len([]rune(r.Label)) > maxLabelLen {
		return fmt.Errorf("session label is longer than %d characters", maxLabelLen)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Updated.IsZero() {
		r.Updated = s.now()
	}
	list := []savedSession{r}
	for _, old := range s.read() {
		if old.Label != r.Label {
			list = append(list, old)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Updated.After(list[j].Updated) })
	return s.write(list)
}

// delete removes a label's record and reports whether there was one.
func (s *sessionStore) delete(label string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.read()
	kept := old[:0:0]
	for _, r := range old {
		if r.Label != label {
			kept = append(kept, r)
		}
	}
	if len(kept) == len(old) {
		return false
	}
	if err := s.write(kept); err != nil {
		log.Printf("[external_agent] cannot forget session %q: %v", label, err)
	}
	return true
}
