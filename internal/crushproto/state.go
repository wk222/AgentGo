package crushproto

import (
	"context"
	"sync"
	"time"
)

// workspace is one client-visible workspace: sessions, message snapshots, the
// SSE subscriber set and pending permission decisions.
type workspace struct {
	id, path, dataDir string

	mu       sync.Mutex
	sessions []*Session
	msgs     map[string][]*Message
	files    map[string][]File // session id -> file versions
	subs     map[chan []byte]struct{}
	pending  map[string]chan bool // permission id -> decision
	asked    map[string]PermissionRequest // permission id -> request still waiting for a decision
	cancels  map[string]context.CancelFunc
	skipAll  bool
	links    map[string]string // session id -> external (AgentGo) session id
	refs     int               // attached clients; guarded by Server.mu, not w.mu

	store        Store // nil: in-memory only
	logf         func(string, ...any)
	saveMu       sync.Mutex // serializes saves
	persistTimer *time.Timer
}

func newWorkspace(id, path, dataDir string) *workspace {
	return &workspace{
		id: id, path: path, dataDir: dataDir,
		links:   map[string]string{},
		msgs:    map[string][]*Message{},
		subs:    map[chan []byte]struct{}{},
		pending: map[string]chan bool{},
		asked:   map[string]PermissionRequest{},
		cancels: map[string]context.CancelFunc{},
	}
}

// publish fans one event out to every SSE subscriber. Slow consumers drop
// events rather than stall the agent.
func (w *workspace) publish(kind, op string, entity any) {
	b, err := envelope(kind, op, entity)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.publishLocked(b)
}

func (w *workspace) publishLocked(frame []byte) {
	for ch := range w.subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

// subscribe attaches a subscriber. replay holds what a client that was not
// connected when it happened still has to be told: permission requests nobody
// has answered yet. Everything else is a snapshot the client can re-read.
func (w *workspace) subscribe() (ch chan []byte, replay [][]byte, cancel func()) {
	ch = make(chan []byte, 512)
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	for _, req := range w.asked {
		if b, err := envelope("permission_request", "created", req); err == nil {
			replay = append(replay, b)
		}
	}
	w.mu.Unlock()
	return ch, replay, func() {
		w.mu.Lock()
		delete(w.subs, ch)
		w.mu.Unlock()
	}
}

func (w *workspace) createSession(title string) Session {
	now := nowSec()
	s := &Session{ID: newID(), Title: title, CreatedAt: now, UpdatedAt: now}
	w.mu.Lock()
	w.sessions = append(w.sessions, s)
	out := *s
	w.dirtyLocked()
	w.mu.Unlock()
	return out
}

func (w *workspace) session(id string) (Session, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, s := range w.sessions {
		if s.ID == id {
			out := *s
			out.IsBusy = w.cancels[id] != nil
			return out, true
		}
	}
	return Session{}, false
}

func (w *workspace) listSessions() []Session {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Session, 0, len(w.sessions))
	for _, s := range w.sessions {
		c := *s
		c.IsBusy = w.cancels[s.ID] != nil
		out = append(out, c)
	}
	return out
}

func (w *workspace) deleteSession(id string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, s := range w.sessions {
		if s.ID == id {
			w.sessions = append(w.sessions[:i], w.sessions[i+1:]...)
			delete(w.msgs, id)
			delete(w.files, id)
			delete(w.links, id)
			w.dirtyLocked()
			return true
		}
	}
	return false
}

// updateSession mutates a session under the lock and publishes session/updated.
func (w *workspace) updateSession(id string, fn func(*Session)) {
	w.mu.Lock()
	var snap *Session
	for _, s := range w.sessions {
		if s.ID == id {
			fn(s)
			s.UpdatedAt = nowSec()
			c := *s
			c.IsBusy = w.cancels[id] != nil
			snap = &c
		}
	}
	var frame []byte
	if snap != nil {
		frame, _ = envelope("session", "updated", snap)
		w.publishLocked(frame)
		w.dirtyLocked()
	}
	w.mu.Unlock()
}

func cloneMessage(m *Message) *Message {
	c := *m
	c.Parts = make([]Part, len(m.Parts))
	for i, p := range m.Parts {
		d := make(map[string]any, len(p.Data))
		for k, v := range p.Data {
			d[k] = v
		}
		c.Parts[i] = Part{Type: p.Type, Data: d}
	}
	return &c
}

// putMessage stores a snapshot of m (replacing an earlier one with the same id)
// and publishes message/<op>. The caller keeps ownership of m.
func (w *workspace) putMessage(m *Message, op string) {
	snap := cloneMessage(m)
	w.mu.Lock()
	list := w.msgs[snap.SessionID]
	replaced := false
	for i, old := range list {
		if old.ID == snap.ID {
			list[i], replaced = snap, true
			break
		}
	}
	if !replaced {
		list = append(list, snap)
	}
	w.msgs[snap.SessionID] = list
	frame, _ := envelope("message", op, snap)
	w.publishLocked(frame)
	w.dirtyLocked()
	w.mu.Unlock()
}

func (w *workspace) messages(sessionID string) []*Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*Message, 0, len(w.msgs[sessionID]))
	for _, m := range w.msgs[sessionID] {
		out = append(out, cloneMessage(m))
	}
	return out
}

func (w *workspace) userMessages(sessionID string) []*Message {
	var out []*Message
	w.mu.Lock()
	defer w.mu.Unlock()
	for sid, list := range w.msgs {
		if sessionID != "" && sid != sessionID {
			continue
		}
		for _, m := range list {
			if m.Role == "user" {
				out = append(out, cloneMessage(m))
			}
		}
	}
	return out
}
