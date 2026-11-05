package agent

import (
	"context"
	"strings"
)

// ExpertResolver says which expert a conversation runs as, in the form of the
// text to add to its system prompt; "" means none. The runner calls it once at
// the start of every run, so an expert file edited between two messages applies
// to the second one. The agent package does not know what an expert is: the host
// builds this function from the session store and the expert library.
type ExpertResolver func(sessionID string) string

type expertCtxKey struct{}

// withExpertBlock puts the expert text of this run in ctx. An empty block still
// replaces what ctx carried, so a conversation without an expert never picks up
// the persona of the conversation that started it (session_message, schedules).
func withExpertBlock(ctx context.Context, block string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, expertCtxKey{}, strings.TrimSpace(block))
}

func expertBlockFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(expertCtxKey{}).(string)
	return s
}

// SetExpertResolver installs the function that tells which expert a
// conversation runs as. nil removes it.
func (r *Runner) SetExpertResolver(fn ExpertResolver) {
	if r == nil {
		return
	}
	r.expertMu.Lock()
	r.expertResolver = fn
	r.expertMu.Unlock()
}

// expertBlockFor is the text that makes sessionID run as its expert, or "".
// ExpertBlock is the block a run of this conversation would carry in its system
// prompt: "" when the conversation has no expert. Hosts and tests use it to see
// what the expert adds without starting a run.
func (r *Runner) ExpertBlock(sessionID string) string { return r.expertBlockFor(sessionID) }

func (r *Runner) expertBlockFor(sessionID string) string {
	if r == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	r.expertMu.RLock()
	fn := r.expertResolver
	r.expertMu.RUnlock()
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(fn(sessionID))
}

// runHints is what is added to the system prompt of a run: the guidance of its
// mode, then the rules of its expert. The middleware checks for this whole text
// before adding it, so it must not change between the model calls of one run.
func runHints(ctx context.Context, sm SessionMode) string {
	hint := sm.ModeHints()
	if x := expertBlockFromContext(ctx); x != "" {
		hint += "\n\n" + x
	}
	return hint
}
