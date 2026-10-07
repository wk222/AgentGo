package agent

import "context"

// ToolObserver receives the full detail of every tool call of one run (call id,
// arguments, output). Unlike EmitTrace — a name-only status ping for the
// desktop status bar — this is what frontends such as the Crush TUI render.
type ToolObserver interface {
	ToolStarted(callID, name, args string)
	ToolFinished(callID, name, output string, isError bool)
}

type toolObserverKey struct{}

// WithToolObserver binds obs to the run carried by ctx.
func WithToolObserver(ctx context.Context, obs ToolObserver) context.Context {
	if obs == nil {
		return ctx
	}
	return context.WithValue(ctx, toolObserverKey{}, obs)
}

func toolObserverFrom(ctx context.Context) ToolObserver {
	obs, _ := ctx.Value(toolObserverKey{}).(ToolObserver)
	return obs
}
