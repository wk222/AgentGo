package agent

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/adk"
)

// RunControl tracks per-session ADK cancel handles and mid-turn steer inbox.
type RunControl struct {
	mu         sync.Mutex
	cancels    map[string]adk.AgentCancelFunc
	ctxCancels map[string]context.CancelFunc
	steers     map[string][]string
}

func NewRunControl() *RunControl {
	return &RunControl{
		cancels:    make(map[string]adk.AgentCancelFunc),
		ctxCancels: make(map[string]context.CancelFunc),
		steers:     make(map[string][]string),
	}
}

func (c *RunControl) Set(sessionID string, fn adk.AgentCancelFunc) {
	if c == nil || sessionID == "" || fn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancels[sessionID] = fn
}

func (c *RunControl) SetCtxCancel(sessionID string, fn context.CancelFunc) {
	if c == nil || sessionID == "" || fn == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ctxCancels[sessionID] = fn
}

func (c *RunControl) Clear(sessionID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cancels, sessionID)
	delete(c.ctxCancels, sessionID)
	delete(c.steers, sessionID)
}

// IsRunning reports whether a given session has an active cancellable execution.
func (c *RunControl) IsRunning(sessionID string) bool {
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
