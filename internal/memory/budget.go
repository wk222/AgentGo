package memory

import (
	"agentgo/internal/config"
	"context"
)

// These budgets bound model-visible memory, not persistent storage. Zero
// disables automatic injection; ordinary storage and explicit history remain.
func TotalTokenBudget() int { return max(0, min(32768, config.MemoryTokens.Get())) }
func AutomaticTokenBudget() int {
	return max(0, min(TotalTokenBudget(), config.MemoryAutoTokens.Get()))
}

type promptBudgetKey struct{}

func WithPromptBudget(ctx context.Context, n int) context.Context {
	return context.WithValue(ctx, promptBudgetKey{}, max(0, n))
}
func PromptTokenBudget(ctx context.Context) int {
	if n, ok := ctx.Value(promptBudgetKey{}).(int); ok {
		return min(n, TotalTokenBudget())
	}
	return AutomaticTokenBudget()
}
