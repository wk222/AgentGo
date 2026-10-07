package lifecycle

import (
	"context"
)

type scopeContextKey struct{}

// WithScope binds a lifecycle Scope to a standard Go context.Context.
func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, s)
}

// FromContext extracts the active lifecycle Scope from a context.Context, or creates a standalone one if none exists.
func FromContext(ctx context.Context) Scope {
	if ctx == nil {
		return NewScope()
	}
	if s, ok := ctx.Value(scopeContextKey{}).(Scope); ok && s != nil {
		return s
	}
	return NewScope()
}

// Effect executes an action and registers its cleanup on the context's Scope if available.
func Effect(ctx context.Context, fn EffectFunc) (Disposer, error) {
	s := FromContext(ctx)
	return s.Effect(fn)
}

// OnDispose registers a cleanup on the context's Scope.
func OnDispose(ctx context.Context, d Disposer) Disposer {
	s := FromContext(ctx)
	return s.OnDispose(d)
}
