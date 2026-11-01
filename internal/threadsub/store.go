// Package threadsub binds external thread keys to conversations.
//
// A pull request, an email thread or a chat room is one running conversation
// from the outside, but the agent sees each reply as a separate event. A thread
// key ("github:acme/app#12", "mail:<root-message-id>") is bound to one session,
// so every later event for that thread reaches the session that already knows
// the history, instead of starting from nothing.
//
// Unlike a trigger, which fires once, a binding stays until it is removed and
// takes any number of events.
package threadsub

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxKeyLen        = 200
	maxPerSession    = 50
	maxTotal         = 2000
	sessionPrefix    = "thread:"
	maxSessionIDSize = 240
)

// Sub is one binding.
type Sub struct {
	Key         string `json:"key"`
	SessionID   string `json:"session_id"`
	Title       string `json:"title,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	LastEventAt int64  `json:"last_event_at,omitempty"`
	Events      int    `json:"events"`
}

// Store keeps bindings in one JSON file, rewritten atomically on every change.
type Store struct {
	path string
	mu   sync.Mutex
	subs map[string]Sub
}

// Open loads the store at path. A missing file is an empty store.
func Open(path string) (*Store, error) {
	s := &Store{path: path, subs: map[string]Sub{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Sub
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("threadsub: %s is not readable: %w", path, err)
	}
	for _, sub := range list {
		if sub.Key != "" && sub.SessionID != "" {
			s.subs[sub.Key] = sub
		}
	}
	return s, nil
}

// NormalizeKey trims a key and rejects one that cannot identify a thread.
func NormalizeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("thread key is required")
	}
	if len(key) > maxKeyLen {
		return "", fmt.Errorf("thread key is longer than %d characters", maxKeyLen)
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("thread key has control characters")
		}
	}
	return key, nil
}

// SessionFor is the session a key gets when nothing was bound to it yet. It is
// derived from the key, so the same thread always lands in the same place.
func SessionFor(key string) string {
	var b strings.Builder
	b.WriteString(sessionPrefix)
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-' || r == '_' || r == '.' || r == '#' || r == '@':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	id := b.String()
	if len(id) > maxSessionIDSize {
		id = id[:maxSessionIDSize]
	}
	return id
}

// Bind binds key to sessionID. Binding a key that belongs to a different session
// moves it, which is how a thread is handed over; the previous owner is returned.
func (s *Store) Bind(key, sessionID, title string) (previous string, err error) {
	if key, err = NormalizeKey(key); err != nil {
		return "", err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("session id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, had := s.subs[key]
	if !had {
		if len(s.subs) >= maxTotal {
			return "", fmt.Errorf("too many thread bindings (%d)", maxTotal)
		}
		if s.countLocked(sessionID) >= maxPerSession {
			return "", fmt.Errorf("this session already follows %d threads; unbind some first", maxPerSession)
		}
	} else if old.SessionID != sessionID && s.countLocked(sessionID) >= maxPerSession {
		return "", fmt.Errorf("this session already follows %d threads; unbind some first", maxPerSession)
	}
	sub := Sub{Key: key, SessionID: sessionID, Title: title, CreatedAt: time.Now().Unix()}
	if had {
		sub.CreatedAt, sub.LastEventAt, sub.Events = old.CreatedAt, old.LastEventAt, old.Events
		if title == "" {
			sub.Title = old.Title
		}
		if old.SessionID != sessionID {
			previous = old.SessionID
		}
	}
	s.subs[key] = sub
	if err := s.saveLocked(); err != nil {
		if had {
			s.subs[key] = old
		} else {
			delete(s.subs, key)
		}
		return "", err
	}
	return previous, nil
}

// Unbind removes key. When owner is not empty only that session may remove it.
func (s *Store) Unbind(key, owner string) error {
	key, err := NormalizeKey(key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.subs[key]
	if !ok {
		return fmt.Errorf("no binding for thread %q", key)
	}
	if owner != "" && old.SessionID != owner {
		return errors.New("this thread is bound to another session")
	}
	delete(s.subs, key)
	if err := s.saveLocked(); err != nil {
		s.subs[key] = old
		return err
	}
	return nil
}

// Resolve returns the binding for key.
func (s *Store) Resolve(key string) (Sub, bool) {
	key = strings.TrimSpace(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[key]
	return sub, ok
}

// Delivered records that an event reached the thread's session.
func (s *Store) Delivered(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subs[key]
	if !ok {
		return
	}
	sub.Events++
	sub.LastEventAt = time.Now().Unix()
	s.subs[key] = sub
	_ = s.saveLocked() // the counters are informational; losing one is harmless
}

// List returns the bindings of sessionID, or all of them when it is empty,
// most recently active first.
func (s *Store) List(sessionID string) []Sub {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Sub{}
	for _, sub := range s.subs {
		if sessionID == "" || sub.SessionID == sessionID {
			out = append(out, sub)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].LastEventAt, out[j].LastEventAt
		if a == 0 {
			a = out[i].CreatedAt
		}
		if b == 0 {
			b = out[j].CreatedAt
		}
		if a != b {
			return a > b
		}
		if out[i].Events != out[j].Events {
			return out[i].Events > out[j].Events
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func (s *Store) countLocked(sessionID string) int {
	n := 0
	for _, sub := range s.subs {
		if sub.SessionID == sessionID {
			n++
		}
	}
	return n
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	list := make([]Sub, 0, len(s.subs))
	for _, sub := range s.subs {
		list = append(list, sub)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
