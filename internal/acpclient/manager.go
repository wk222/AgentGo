package acpclient

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultIdle    = 15 * time.Minute
	defaultMaxLive = 4
)

// Manager keeps named sessions alive between prompts, so a later call can carry on with what the
// agent already knows. An unnamed run is one-shot: the agent starts, answers, and is stopped.
// Idle sessions are closed after a while; the number of live agents is capped.
//
// With a store (SetStore) the label of a session that ended without being closed is remembered
// too, and the next call with that label asks the agent to carry on the same session.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*kept
	idle     time.Duration
	maxLive  int
	launch   func(context.Context, Config) (*Conn, error) // replaced by tests
	store    *sessionStore                                // nil: nothing survives the agent process
}

type kept struct {
	conn  *Conn
	agent string
	cfg   Config
	last  time.Time
	timer *time.Timer
}

// SessionInfo describes a named session. Live is false for one whose agent has stopped but that a
// call with the same label can still carry on.
type SessionInfo struct {
	Label      string `json:"label"`
	Agent      string `json:"agent"`
	Cwd        string `json:"cwd"`
	Permission string `json:"permission"`
	IdleSec    int64  `json:"idle_sec"`
	Live       bool   `json:"live"`
}

func NewManager() *Manager {
	return &Manager{sessions: map[string]*kept{}, idle: defaultIdle, maxLive: defaultMaxLive, launch: Launch}
}

// SetStore remembers named sessions in the JSON file at path, so that after their agent was stopped
// (idle timeout, the cap on live agents, the app closing) a call with the same label carries on
// the session instead of starting from nothing. "" turns it off.
func (m *Manager) SetStore(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if path == "" {
		m.store = nil
		return
	}
	m.store = newSessionStore(path)
}

func (m *Manager) savedStore() *sessionStore {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.store
}

// resumable reports whether a session saved under the permission `saved` may be carried on under
// `now`. The agent keeps its own memory of what it was allowed to do, so a session that ran with
// more freedom than is asked for now starts over instead; one that ran with less may go on.
func resumable(saved string, now Permission) bool {
	p, err := ParsePermission(saved)
	return err == nil && p <= now
}

// samePath compares directories the way the OS does.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Default is the manager the external_agent tool uses.
var Default = NewManager()

// Run sends prompt to the agent in cfg. With a label the session is kept (and reused by the next
// call with the same label); without one the agent is stopped afterwards.
func (m *Manager) Run(ctx context.Context, label, agent string, cfg Config, prompt string, onEvent func(Event)) (*Turn, error) {
	if n := len([]rune(label)); n > maxLabelLen {
		return nil, fmt.Errorf("the session label is %d characters long; the limit is %d", n, maxLabelLen)
	}
	if label == "" {
		conn, err := m.launch(ctx, cfg)
		if err != nil {
			return nil, err
		}
		defer conn.Close()
		return conn.Prompt(ctx, prompt, onEvent)
	}
	conn, err := m.acquire(ctx, label, agent, cfg)
	if err != nil {
		return nil, err
	}
	turn, err := conn.Prompt(ctx, prompt, onEvent)
	m.touch(label)
	if err != nil && !conn.Alive() {
		m.drop(label, conn) // a dead agent must not be handed out again
	}
	if err == nil {
		m.remember(label, agent, cfg, conn)
	}
	return turn, err
}

// remember keeps what a later call needs to carry on this session. An agent that cannot load
// sessions gets no record: it would promise something that cannot happen.
func (m *Manager) remember(label, agent string, cfg Config, conn *Conn) {
	st := m.savedStore()
	if st == nil || !conn.CanResume() || conn.SessionID() == "" {
		return
	}
	err := st.put(savedSession{Label: label, Agent: agent, Cwd: cfg.Cwd, SessionID: conn.SessionID(), Permission: cfg.Permission.String()})
	if err != nil {
		log.Printf("[external_agent] cannot keep session %q for later: %v", label, err)
	}
}

func (m *Manager) acquire(ctx context.Context, label, agent string, cfg Config) (*Conn, error) {
	m.mu.Lock()
	if k := m.sessions[label]; k != nil {
		if k.conn.Alive() {
			if k.agent != agent || k.cfg.Cwd != cfg.Cwd || k.cfg.Permission != cfg.Permission {
				m.mu.Unlock()
				return nil, fmt.Errorf("session %q is already open with %s in %s (%s); close it or pick another label",
					label, k.agent, k.cfg.Cwd, k.cfg.Permission)
			}
			k.last = time.Now()
			if k.timer != nil {
				k.timer.Stop() // not idle while a turn is about to run; touch() re-arms it
			}
			conn := k.conn
			m.mu.Unlock()
			return conn, nil
		}
		delete(m.sessions, label) // it died since the last turn
	}
	var evict *Conn
	if len(m.sessions) >= m.maxLive {
		evict = m.evictOldestLocked()
	}
	m.mu.Unlock()
	if evict != nil {
		evict.Close()
	}

	// No live agent under this label: if an earlier one left a session behind, carry it on. A record
	// for another agent or directory belongs to a different job that reused the label; it is left
	// to be replaced by this one.
	if st := m.savedStore(); st != nil {
		if rec, ok := st.get(label); ok && rec.Agent == agent && samePath(rec.Cwd, cfg.Cwd) && resumable(rec.Permission, cfg.Permission) {
			cfg.ResumeSessionID = rec.SessionID
		}
	}
	conn, err := m.launch(ctx, cfg)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if other := m.sessions[label]; other != nil && other.conn.Alive() { // two calls raced to open it
		go conn.Close()
		return other.conn, nil
	}
	m.sessions[label] = &kept{conn: conn, agent: agent, cfg: cfg, last: time.Now()}
	return conn, nil
}

func (m *Manager) evictOldestLocked() *Conn {
	var oldest string
	for l, k := range m.sessions {
		if oldest == "" || k.last.Before(m.sessions[oldest].last) {
			oldest = l
		}
	}
	if oldest == "" {
		return nil
	}
	k := m.sessions[oldest]
	delete(m.sessions, oldest)
	if k.timer != nil {
		k.timer.Stop()
	}
	return k.conn
}

// touch marks a turn finished and starts the idle countdown.
func (m *Manager) touch(label string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.sessions[label]
	if k == nil {
		return
	}
	k.last = time.Now()
	if k.timer != nil {
		k.timer.Stop()
	}
	conn := k.conn
	k.timer = time.AfterFunc(m.idle, func() { m.drop(label, conn); conn.Close() })
}

// drop forgets a session if it still is conn (the label may have been reused since).
func (m *Manager) drop(label string, conn *Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if k := m.sessions[label]; k != nil && k.conn == conn {
		if k.timer != nil {
			k.timer.Stop()
		}
		delete(m.sessions, label)
	}
}

// Close ends one named session for good: the agent is stopped and the saved session is forgotten,
// so the label starts from nothing next time. It reports whether there was anything to end.
func (m *Manager) Close(label string) bool {
	m.mu.Lock()
	k := m.sessions[label]
	delete(m.sessions, label)
	st := m.store
	m.mu.Unlock()
	forgot := st != nil && st.delete(label)
	if k == nil {
		return forgot
	}
	if k.timer != nil {
		k.timer.Stop()
	}
	k.conn.Close()
	return true
}

// CloseAll ends every kept session, for shutdown.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	all := m.sessions
	m.sessions = map[string]*kept{}
	m.mu.Unlock()
	for _, k := range all {
		if k.timer != nil {
			k.timer.Stop()
		}
		k.conn.Close()
	}
}

// List describes the named sessions by label: the live ones, then (with a store) those whose
// agent has stopped but that the same label can still carry on.
func (m *Manager) List() []SessionInfo {
	m.mu.Lock()
	out := make([]SessionInfo, 0, len(m.sessions))
	live := map[string]bool{}
	for label, k := range m.sessions {
		if !k.conn.Alive() {
			continue
		}
		live[label] = true
		out = append(out, SessionInfo{Label: label, Agent: k.agent, Cwd: k.cfg.Cwd,
			Permission: k.cfg.Permission.String(), IdleSec: int64(time.Since(k.last) / time.Second), Live: true})
	}
	st := m.store
	m.mu.Unlock()
	if st != nil {
		for _, r := range st.list() {
			if !live[r.Label] {
				out = append(out, SessionInfo{Label: r.Label, Agent: r.Agent, Cwd: r.Cwd, Permission: r.Permission,
					IdleSec: int64(time.Since(r.Updated) / time.Second)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
