package governance

import (
	"sync"
	"time"
)

// ToolCallTracker counts recent tool invocations per session+tool (PyBot rate-limit stage).
type ToolCallTracker struct {
	mu      sync.Mutex
	windows map[string][]int64 // key session|tool -> unix seconds
	ttl     time.Duration
}

func NewToolCallTracker() *ToolCallTracker {
	return &ToolCallTracker{
		windows: make(map[string][]int64),
		ttl:     10 * time.Minute,
	}
}

func trackerKey(sessionID, toolName string) string {
	return sessionID + "|" + toolName
}

func (t *ToolCallTracker) RecentCount(sessionID, toolName string) int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneLocked(sessionID, toolName)
	return len(t.windows[trackerKey(sessionID, toolName)])
}

func (t *ToolCallTracker) Record(sessionID, toolName string) {
	if t == nil || toolName == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := trackerKey(sessionID, toolName)
	t.pruneLocked(sessionID, toolName)
	t.windows[key] = append(t.windows[key], time.Now().Unix())
}

func (t *ToolCallTracker) pruneLocked(sessionID, toolName string) {
	key := trackerKey(sessionID, toolName)
	cutoff := time.Now().Add(-t.ttl).Unix()
	ts := t.windows[key]
	if len(ts) == 0 {
		return
	}
	out := ts[:0]
	for _, v := range ts {
		if v >= cutoff {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		delete(t.windows, key)
		return
	}
	t.windows[key] = out
}
