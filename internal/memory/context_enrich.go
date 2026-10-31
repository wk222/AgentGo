package memory

import (
	"agentgo/internal/promptbudget"
	"context"
)

// EnrichedEngine wraps HybridEngine to inject recent journals into ContextPrompt (JOURNAL recall).
type EnrichedEngine struct {
	*HybridEngine
}

func NewEnrichedEngine(h *HybridEngine) *EnrichedEngine {
	if h == nil {
		return nil
	}
	return &EnrichedEngine{HybridEngine: h}
}

// NewEnrichedPipeline is an alias for NewEnrichedEngine (backward compatible name).
func NewEnrichedPipeline(h *HybridEngine) *EnrichedEngine {
	return NewEnrichedEngine(h)
}

func (p *EnrichedEngine) Link(ctx context.Context, sourceID, targetID, relation string) error {
	if p == nil || p.HybridEngine == nil {
		return nil
	}
	return p.HybridEngine.Link(ctx, sourceID, targetID, relation)
}

func (p *EnrichedEngine) ContextPrompt(ctx context.Context, sessionID string) (string, error) {
	base, err := p.HybridEngine.ContextPrompt(ctx, sessionID)
	if err != nil {
		return "", err
	}
	// Task-aware recall already searches journals. Appending the latest journals
	// regardless of relevance would duplicate them and evade the shared budget.
	if automaticRecall(ctx) {
		return base, nil
	}
	journals, err := p.sqlite.ListByModality(ctx, modalityJournal, sessionID, 3)
	if err != nil {
		return "", err
	}
	remaining := PromptTokenBudget(ctx) - promptbudget.Estimate(base) - 1
	extra := packContextRecords(journals, remaining)
	if base == "" {
		return extra, nil
	}
	if extra == "" {
		return base, nil
	}
	return base + "\n" + extra, nil
}
