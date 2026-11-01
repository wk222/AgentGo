package agent

import (
	"context"
	"sync/atomic"
	"time"
)

// runActivity is what the watchdog looks at: when a run last showed a sign of
// life (an ADK event, a streamed chunk, a tool starting or ending) and how many
// tools it is waiting on. A long shell command is a tool in flight, not a hang,
// so the two limits differ (see Watchdog).
type runActivity struct {
	last     atomic.Int64 // unix nanoseconds
	inflight atomic.Int32
}

type runActivityKey struct{}

func newRunActivity() *runActivity {
	a := &runActivity{}
	a.touch()
	return a
}

// withRunActivity binds an activity record to ctx unless one is already there.
func withRunActivity(ctx context.Context) context.Context {
	if runActivityFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, runActivityKey{}, newRunActivity())
}

func runActivityFrom(ctx context.Context) *runActivity {
	a, _ := ctx.Value(runActivityKey{}).(*runActivity)
	return a
}

func (a *runActivity) touch() {
	if a != nil {
		a.last.Store(time.Now().UnixNano())
	}
}

func (a *runActivity) toolStart() {
	if a != nil {
		a.inflight.Add(1)
		a.touch()
	}
}

func (a *runActivity) toolEnd() {
	if a != nil {
		a.inflight.Add(-1)
		a.touch()
	}
}

// idleSince reports how long the run has shown no sign of life, and whether it
// is waiting on a tool.
func (a *runActivity) idleSince(now time.Time) (idle time.Duration, inTool bool) {
	if a == nil {
		return 0, false
	}
	return now.Sub(time.Unix(0, a.last.Load())), a.inflight.Load() > 0
}

// track registers the activity of the run now executing in sessionID.
func (c *RunControl) track(sessionID string, a *runActivity) {
	sessionID = runControlSessionID(sessionID)
	if c == nil || sessionID == "" || a == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.activity == nil {
		c.activity = make(map[string]*runActivity)
	}
	c.activity[sessionID] = a
}

// Activity returns the registered activity of a session's current run, or nil.
func (c *RunControl) Activity(sessionID string) *runActivity {
	sessionID = runControlSessionID(sessionID)
	if c == nil || sessionID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.activity[sessionID]
}
