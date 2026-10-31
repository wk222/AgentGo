package agent

import (
	"context"
	"strings"

	"agentgo/internal/evidence"
	"agentgo/internal/governance"
)

type sessionKey struct{}

// WithSessionID labels non-executing helpers, such as looking up fork task
// directories. Execution entry points must use BindRunContext.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionKey{}, sessionID)
}

// BindRunContext attaches session ids for memory spine and governance rate-limit tracking.
func BindRunContext(ctx context.Context, sessionID string) context.Context {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "desktop"
	}
	ctx = WithSessionID(ctx, sessionID)
	ctx = context.WithValue(ctx, runIDKey{}, newRunID())
	traceCtx := ctx
	ctx = governance.WithAuditObserver(ctx, func(callID, action, tool, result, risk, explanation string) {
		recordCallTrace(traceCtx, callID, tool, TraceStagePolicy, 0, map[string]any{
			"action": action, "result": result, "risk": risk, "explanation": explanation,
		})
	})
	return evidence.WithRun(governance.WithExecution(governance.WithSessionID(ctx, sessionID)), sessionID)
}

func SessionIDFromContext(ctx context.Context) string {
	s, _ := ctx.Value(sessionKey{}).(string)
	if s == "" {
		return "desktop"
	}
	return s
}
