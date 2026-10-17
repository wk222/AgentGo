package bridge

import (
	"context"

	"agentgo/internal/engine"
)

type eventTapKey struct{}

// withEventTap lets a caller of a resumed run receive its live events (tool
// calls, tokens, file changes) in addition to the desktop subscribers. A
// resumed segment otherwise reports only through the desktop event bus, which
// a terminal has no window on.
func withEventTap(ctx context.Context, tap engine.EventSink) context.Context {
	if tap == nil {
		return ctx
	}
	return context.WithValue(ctx, eventTapKey{}, tap)
}

func eventTapFrom(ctx context.Context) engine.EventSink {
	tap, _ := ctx.Value(eventTapKey{}).(engine.EventSink)
	return tap
}

// ResolveApprovalTap settles an approval like ResolveApproval and streams the
// continued run's events to tap while it executes.
func (s *AppService) ResolveApprovalTap(approvalID string, approved bool, note string, tap engine.EventSink) map[string]any {
	return s.resolveApproval(withEventTap(context.Background(), tap), approvalID, approved, note, "desktop_user", "")
}
