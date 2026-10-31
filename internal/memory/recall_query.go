package memory

import (
	"context"
	"strings"
)

type recallQueryKey struct{}

// WithRecallQuery supplies the current task to automatic context recall. This
// path does not need a separate LLM query rewrite before the user's model call.
func WithRecallQuery(ctx context.Context, query string) context.Context {
	query = strings.TrimSpace(query)
	runes := []rune(query)
	if len(runes) > 1600 {
		query = string(runes[:800]) + " … " + string(runes[len(runes)-800:])
	}
	return context.WithValue(ctx, recallQueryKey{}, query)
}
func RecallQueryFromContext(ctx context.Context) string {
	query, _ := ctx.Value(recallQueryKey{}).(string)
	return query
}
func automaticRecall(ctx context.Context) bool {
	_, ok := ctx.Value(recallQueryKey{}).(string)
	return ok
}
