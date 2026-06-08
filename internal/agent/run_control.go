package agent

import (
	"context"
	"sync"

	"github.com/cloudwego/eino/adk"
)

// RunControl tracks per-session ADK cancel handles (UI stop button).
type RunControl struct {
	mu         sync.Mutex
	cancels    map[string]adk.AgentCancelFunc
	ctxCancels map[string]context.CancelFunc
}

func NewRunControl() *RunControl {
	return &RunControl{
		cancels:    make(map[string]adk.AgentCancelFunc),
		ctxCancels: make(map[string]context.CancelFunc),
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
