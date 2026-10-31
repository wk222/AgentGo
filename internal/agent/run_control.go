package agent

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
)

// RunControl tracks per-session ADK cancel handles and mid-turn steer inbox.
type RunControl struct {
	mu         sync.Mutex
	cancels    map[string]adk.AgentCancelFunc
	ctxCancels map[string]context.CancelFunc
	steers     map[string][]string
	// started is when a session's current run first registered a cancel handle.
	// It lives and dies with that registration, so it answers "how long has this
	// been running" without a second clock to keep in step.
	started map[string]time.Time
}

func NewRunControl() *RunControl {
	return &RunControl{
		cancels:    make(map[string]adk.AgentCancelFunc),
		ctxCancels: make(map[string]context.CancelFunc),
		steers:     make(map[string][]string),
		started:    make(map[string]time.Time),
	}
}

// markStarted records the first registration of a run. Callers hold c.mu.
func (c *RunControl) markStarted(sessionID string) {
	if c.started == nil {
		c.started = make(map[string]time.Time)
	}
	if _, ok := c.started[sessionID]; !ok {
		c.started[sessionID] = time.Now()
	}
}

func (c *RunControl) Set(sessionID string, fn adk.AgentCancelFunc) {
	sessionID = runControlSessionID(sessionID)
	if c == nil || sessionID == "" || fn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancels[sessionID] = fn
	c.markStarted(sessionID)
}

func (c *RunControl) SetCtxCancel(sessionID string, fn context.CancelFunc) {
	sessionID = runControlSessionID(sessionID)
	if c == nil || sessionID == "" || fn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ctxCancels[sessionID] = fn
	c.markStarted(sessionID)
}

func (c *RunControl) Clear(sessionID string) {
	sessionID = runControlSessionID(sessionID)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cancels, sessionID)
	delete(c.ctxCancels, sessionID)
	delete(c.steers, sessionID)
	delete(c.started, sessionID)
}

// RunningSession is one session with a cancellable run, and when it started.
type RunningSession struct {
	SessionID string
	StartedAt time.Time
}

// Running lists the sessions that have a cancellable run right now, oldest first.
// This is the one list that also covers runs resumed after an approval or an
// answer, which do not go through the engine router.
func (c *RunControl) Running() []RunningSession {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]struct{}, len(c.cancels)+len(c.ctxCancels))
	out := make([]RunningSession, 0, len(c.cancels)+len(c.ctxCancels))
	add := func(id string) {
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		out = append(out, RunningSession{SessionID: id, StartedAt: c.started[id]})
	}
	for id := range c.cancels {
		add(id)
	}
	for id := range c.ctxCancels {
		add(id)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.Before(out[j].StartedAt)
		}
		return out[i].SessionID < out[j].SessionID
	})
	return out
}

// IsRunning reports whether a given session has an active cancellable execution.
func (c *RunControl) IsRunning(sessionID string) bool {
	sessionID = runControlSessionID(sessionID)
	if c == nil || sessionID == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok1 := c.cancels[sessionID]
	_, ok2 := c.ctxCancels[sessionID]
	return ok1 || ok2
}

// AddSteer queues a mid-turn instruction for a currently executing session.
func (c *RunControl) AddSteer(sessionID, instruction string) bool {
	sessionID = runControlSessionID(sessionID)
	if c == nil || sessionID == "" || instruction == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.cancels[sessionID]; !ok {
		if _, okCtx := c.ctxCancels[sessionID]; !okCtx {
			return false
		}
	}
	c.steers[sessionID] = append(c.steers[sessionID], instruction)
	return true
}

// DrainSteers returns and clears all queued steer instructions for a session.
func (c *RunControl) DrainSteers(sessionID string) []string {
	sessionID = runControlSessionID(sessionID)
	if c == nil || sessionID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	list := c.steers[sessionID]
	if len(list) > 0 {
		delete(c.steers, sessionID)
	}
	return list
}

// Active reports how many sessions currently have a cancellable run.
func (c *RunControl) Active() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make(map[string]struct{}, len(c.cancels)+len(c.ctxCancels))
	for id := range c.cancels {
		ids[id] = struct{}{}
	}
	for id := range c.ctxCancels {
		ids[id] = struct{}{}
	}
	return len(ids)
}

// CancelSession requests ADK agent cancellation for an active run.
func (c *RunControl) CancelSession(sessionID string) bool {
	sessionID = runControlSessionID(sessionID)
	if c == nil || sessionID == "" {
		return false
	}
	c.mu.Lock()
	fn := c.cancels[sessionID]
	ctxFn := c.ctxCancels[sessionID]
	c.mu.Unlock()

	called := false
	if fn != nil {
		_, _ = fn(adk.WithAgentCancelMode(adk.CancelImmediate))
		called = true
	}
	if ctxFn != nil {
		ctxFn()
		called = true
	}
	return called
}

func runControlSessionID(id string) string {
	return strings.TrimPrefix(strings.TrimSpace(id), "agentic:")
}
