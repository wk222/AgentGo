package acpclient

import (
	"context"
	"fmt"
	"sort"
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
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*kept
	idle     time.Duration
	maxLive  int
	launch   func(context.Context, Config) (*Conn, error) // replaced by tests
}

type kept struct {
	conn  *Conn
	agent string
	cfg   Config
	last  time.Time
	timer *time.Timer
}

// SessionInfo describes a live named session.
type SessionInfo struct {
	Label      string `json:"label"`
	Agent      string `json:"agent"`
	Cwd        string `json:"cwd"`
	Permission string `json:"permission"`
	IdleSec    int64  `json:"idle_sec"`
}

func NewManager() *Manager {
	return &Manager{sessions: map[string]*kept{}, idle: defaultIdle, maxLive: defaultMaxLive, launch: Launch}
}

// Default is the manager the external_agent tool uses.
var Default = NewManager()

// Run sends prompt to the agent in cfg. With a label the session is kept (and reused by the next
// call with the same label); without one the agent is stopped afterwards.
func (m *Manager) Run(ctx context.Context, label, agent string, cfg Config, prompt string, onEvent func(Event)) (*Turn, error) {
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
	return turn, err
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

// Close ends one named session; it reports whether there was one.
func (m *Manager) Close(label string) bool {
	m.mu.Lock()
	k := m.sessions[label]
	delete(m.sessions, label)
	m.mu.Unlock()
	if k == nil {
		return false
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

// List describes the live named sessions, oldest label first.
func (m *Manager) List() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for label, k := range m.sessions {
		if !k.conn.Alive() {
			continue
		}
		out = append(out, SessionInfo{Label: label, Agent: k.agent, Cwd: k.cfg.Cwd,
			Permission: k.cfg.Permission.String(), IdleSec: int64(time.Since(k.last) / time.Second)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}
