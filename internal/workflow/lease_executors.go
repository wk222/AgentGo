package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// AcquireLeaseNodeExecutor waits until an exclusive lease is held.
type AcquireLeaseNodeExecutor struct{}

func (e *AcquireLeaseNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	key := leaseKey(n, rc)
	owner := rc.RunID
	if n.Config != nil {
		if o, ok := n.Config["owner"].(string); ok && o != "" {
			owner = expandTemplateVars(o, input, last, rc.Vars)
		}
	}
	ttl := 5 * time.Minute
	if n.Config != nil {
		switch v := n.Config["ttl_ms"].(type) {
		case float64:
			ttl = time.Duration(v) * time.Millisecond
		case int:
			ttl = time.Duration(v) * time.Millisecond
		}
	}
	store := rc.LeaseStore
	if store == nil {
		store = DefaultLeaseStore()
	}
	if err := store.WaitAcquire(ctx, key, owner, ttl, 300*time.Millisecond); err != nil {
		return "", fmt.Errorf("lease node %s: %w", n.ID, err)
	}
	return fmt.Sprintf(`{"lease":%q,"owner":%q}`, key, owner), nil
}

// ReleaseLeaseNodeExecutor releases a lease.
type ReleaseLeaseNodeExecutor struct{}

func (e *ReleaseLeaseNodeExecutor) Execute(ctx context.Context, n Node, input, last string, rc RunContext) (string, error) {
	key := leaseKey(n, rc)
	owner := rc.RunID
	if n.Config != nil {
		if o, ok := n.Config["owner"].(string); ok && o != "" {
			owner = expandTemplateVars(o, input, last, rc.Vars)
		}
	}
	store := rc.LeaseStore
	if store == nil {
		store = DefaultLeaseStore()
	}
	store.Release(key, owner)
	return last, nil
}

func leaseKey(n Node, rc RunContext) string {
	key := strings.TrimSpace(n.ToolName)
	if key == "" && n.Config != nil {
		if k, ok := n.Config["key"].(string); ok {
			key = k
		}
	}
	if key == "" {
		key = "workflow:" + rc.RunID
	}
	return key
}
