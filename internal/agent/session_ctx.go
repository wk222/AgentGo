package agent

import (
	"context"

	"agentgo/internal/governance"
)

type sessionKey struct{}

func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionKey{}, sessionID)
}

// BindRunContext attaches session ids for memory spine and governance rate-limit tracking.
func BindRunContext(ctx context.Context, sessionID string) context.Context {
	ctx = WithSessionID(ctx, sessionID)
	return governance.WithSessionID(ctx, sessionID)
}

func SessionIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(sessionKey{}).(string)
	if s == "" {
		return "desktop"
	}
	return s
}
