package governance

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

type executionScopeKey struct{}
type executionScope struct {
	mu sync.Mutex
	id string
}

// WithExecution shares a root execution across nested agents, independently of
// their session IDs. A new parent context starts an independent turn.
func WithExecution(ctx context.Context) context.Context {
	return WithExecutionID(ctx, "")
}

// WithExecutionID reuses the host's existing run ID when one is available.
// Nested runs retain their parent scope even if they have their own run IDs.
func WithExecutionID(ctx context.Context, id string) context.Context {
	if _, ok := ctx.Value(executionScopeKey{}).(*executionScope); ok {
		return ctx
	}
	if id == "" {
		id = uuid.NewString()
	}
	return context.WithValue(ctx, executionScopeKey{}, &executionScope{id: id})
}

func executionID(ctx context.Context, workspace string) string {
	if s, ok := ctx.Value(executionScopeKey{}).(*executionScope); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.id
	}
	// Direct workflow/legacy callers retain a session-scoped recent-call limit.
	return ComputePayloadHash(workspace, SessionIDFromContext(ctx))
}

func restoreExecution(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	} // checkpoints written before execution scopes
	if s, ok := ctx.Value(executionScopeKey{}).(*executionScope); ok {
		s.mu.Lock()
		s.id = id
		s.mu.Unlock()
		return ctx
	}
	return context.WithValue(ctx, executionScopeKey{}, &executionScope{id: id})
}
