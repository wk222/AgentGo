package governance

import "context"

type sessionCtxKey struct{}

// WithSessionID attaches session scope for rate-limit / loop tracking in the policy pipeline.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

// SessionIDFromContext returns the session id for governance tracking (default "desktop").
func SessionIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(sessionCtxKey{}).(string)
	if s == "" {
		return "desktop"
	}
	return s
}
