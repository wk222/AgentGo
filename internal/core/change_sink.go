package core

import (
	"sync"

	"agentgo/internal/engine"
	"agentgo/internal/workflow"
)

// ChangeSink folds an engine event stream into a coding_agent node result: the
// tool-call count, the files changed (first "before", latest "after") and the
// approval the run stopped for. Engines that emit file_change events are folded
// in as well as the tool calls the ChangeWatcher derives changes from.
type ChangeSink struct {
	WorkDir   string
	EditTools map[string]bool // see ChangeWatcher.EditTools

	mu       sync.Mutex
	watch    ChangeWatcher
	order    []string
	changes  map[string]*workflow.CodingChange
	calls    int
	approval struct{ id, tool, args string }
}

func (c *ChangeSink) w() *ChangeWatcher {
	c.watch.WorkDir, c.watch.EditTools = c.WorkDir, c.EditTools
	return &c.watch
}

// Calls is how many tool calls the run made (including seeded ones).
func (c *ChangeSink) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// Approval is the approval request the run paused on, if any.
func (c *ChangeSink) Approval() (id, tool, args string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.approval.id, c.approval.tool, c.approval.args
}

// Seed starts the sink from the work done before an approval pause.
func (c *ChangeSink) Seed(calls int, changes []workflow.CodingChange) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = calls
	for _, ch := range changes {
		c.record(ch.Path, ch.Before, ch.After)
	}
}

// Changes lists the changed files in first-seen order.
func (c *ChangeSink) Changes() []workflow.CodingChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]workflow.CodingChange, 0, len(c.order))
	for _, p := range c.order {
		out = append(out, *c.changes[p])
	}
	return out
}

// record must be called with c.mu held.
func (c *ChangeSink) record(path, before, after string) {
	if path == "" {
		return
	}
	if ch, ok := c.changes[path]; ok {
		ch.After = after // keep the first "before", track the latest "after"
		return
	}
	if c.changes == nil {
		c.changes = map[string]*workflow.CodingChange{}
	}
	c.changes[path] = &workflow.CodingChange{Path: path, Before: before, After: after}
	c.order = append(c.order, path)
}

// settleLocked turns the snapshots of a finished call into recorded changes.
func (c *ChangeSink) settleLocked(id string, isErr bool) {
	for _, d := range c.w().Finish(id, isErr) {
		if d.Before != d.After {
			c.record(d.Path, d.Before, d.After)
		}
	}
}

// Watch/Settle bracket a call the engine will not report itself (the tool that
// an approval is about to release).
func (c *ChangeSink) Watch(id, name, args string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.w().Start(id, name, args)
}

func (c *ChangeSink) Settle(id string, isErr bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settleLocked(id, isErr)
}

// Emit implements engine.EventSink.
func (c *ChangeSink) Emit(e engine.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	str := func(k string) string { s, _ := e.Payload[k].(string); return s }
	switch e.Type {
	case engine.EventToolCall:
		c.calls++
		c.w().Start(str("id"), str("name"), str("arguments"))
	case engine.EventToolResult:
		isErr, _ := e.Payload["is_error"].(bool)
		c.settleLocked(str("id"), isErr)
	case engine.EventFileChange:
		c.record(str("path"), str("before"), str("after"))
	case engine.EventApprovalRequest:
		c.approval.id, c.approval.tool, c.approval.args = str("approval_id"), str("tool_name"), str("arguments")
	}
}
