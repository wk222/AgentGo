package memory

import (
	"context"
	"strings"
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
		base = ""
	}
	journals, _ := p.sqlite.ListByModality(ctx, modalityJournal, sessionID, 3)
	if len(journals) == 0 {
		return base, nil
	}
	var b strings.Builder
	if base != "" {
		b.WriteString(base)
		b.WriteString("\n")
	}
	b.WriteString("## Recent journals\n")
	for _, j := range journals {
		line := strings.TrimSpace(j.Content)
		if len(line) > 400 {
			line = line[:400] + "..."
		}
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}
