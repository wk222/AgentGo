package agent

import "context"

type textEmitterKey struct{}

// WithTextEmitter supplies streaming output for checkpoint continuations.
func WithTextEmitter(ctx context.Context, emit func(string)) context.Context {
	return context.WithValue(ctx, textEmitterKey{}, emit)
}

func textEmitterFrom(ctx context.Context) func(string) {
	emit, _ := ctx.Value(textEmitterKey{}).(func(string))
	return emit
}
