package core

import "context"

type turnMetaKey struct{}

// WithTurnMeta attaches fields to the user message that Transcript.Begin stores
// for the turn about to start (the id of the workspace checkpoint taken for it,
// for one). Fields added later win over earlier ones with the same key.
func WithTurnMeta(ctx context.Context, meta map[string]any) context.Context {
	if len(meta) == 0 {
		return ctx
	}
	merged := make(map[string]any, len(meta))
	for k, v := range TurnMetaFrom(ctx) {
		merged[k] = v
	}
	for k, v := range meta {
		merged[k] = v
	}
	return context.WithValue(ctx, turnMetaKey{}, merged)
}

// TurnMetaFrom returns the fields attached with WithTurnMeta (nil when none).
func TurnMetaFrom(ctx context.Context) map[string]any {
	m, _ := ctx.Value(turnMetaKey{}).(map[string]any)
	return m
}
