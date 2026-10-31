package agent

import "context"

type modelBudgetKey struct{}
type modelBudget struct{ Window, Output, Input int }

// Model limits describe the selected endpoint, not a guess based on its name.
func withLLMBudget(ctx context.Context, cfg LLMSettings) context.Context {
	if cfg.ContextWindow <= 0 {
		return ctx
	}
	output := modelOutputLimit(cfg)
	// Leave room for provider framing and estimator error as well as generation.
	input := max(1, cfg.ContextWindow-output-max(512, cfg.ContextWindow/50))
	budget := modelBudget{Window: cfg.ContextWindow, Output: output, Input: input}

	return context.WithValue(ctx, modelBudgetKey{}, budget)
}

func modelBudgetFrom(ctx context.Context) modelBudget {
	b, _ := ctx.Value(modelBudgetKey{}).(modelBudget)
	return b
}

func modelOutputLimit(cfg LLMSettings) int {
	if cfg.MaxOutputTokens > 0 {
		return cfg.MaxOutputTokens
	}
	if cfg.ContextWindow > 0 {
		return min(4096, max(1, cfg.ContextWindow/4))
	}
	return 0
}

func constrainPolicyToModel(ctx context.Context, policy CanvasPolicy) CanvasPolicy {
	budget := modelBudgetFrom(ctx)
	if budget.Input <= 0 {
		return policy
	}
	if policy.ContextTokenBudget <= 0 || policy.ContextTokenBudget > budget.Input {
		policy.ContextTokenBudget = budget.Input
	}
	if policy.SummarizationTokens <= 0 || policy.SummarizationTokens > policy.ContextTokenBudget*3/4 {
		policy.SummarizationTokens = policy.ContextTokenBudget * 3 / 4
	}
	return policy
}
