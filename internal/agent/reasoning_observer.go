package agent

import "context"

// ReasoningObserver receives streamed reasoning/thinking tokens from models
// that support reasoning (e.g. DeepSeek-R1, o3, Claude Thinking).
type ReasoningObserver interface {
	ReasoningDelta(delta string)
}

type reasoningObserverKey struct{}

// WithReasoningObserver binds obs to the run carried by ctx.
func WithReasoningObserver(ctx context.Context, obs ReasoningObserver) context.Context {
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, reasoningObserverKey{}, obs)
}

func reasoningObserverFrom(ctx context.Context) ReasoningObserver {
	obs, _ := ctx.Value(reasoningObserverKey{}).(ReasoningObserver)
	return obs
}
