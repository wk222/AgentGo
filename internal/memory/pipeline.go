package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Pipeline wraps SQLiteStore with PyBot-style DISTILL / GC / feedback weighting.
type Pipeline struct {
	store             *SQLiteStore
	ingestHook        func(context.Context, Record) error
	contextPromptHook func(context.Context, string) (string, error)
}

func NewPipeline(store *SQLiteStore) *Pipeline {
	return &Pipeline{store: store}
}

// Store exposes the underlying SQLite store.
func (p *Pipeline) Store() *SQLiteStore {
	if p == nil {
		return nil
	}
	return p.store
}

// SetIngestHook routes Ingest through HybridEngine (SQLite + embedding + Milvus).
func (p *Pipeline) SetIngestHook(fn func(context.Context, Record) error) {
	p.ingestHook = fn
}

// SetContextPromptHook routes recall through HybridEngine (FTS + vector).
func (p *Pipeline) SetContextPromptHook(fn func(context.Context, string) (string, error)) {
	p.contextPromptHook = fn
}

func (p *Pipeline) Ingest(ctx context.Context, record Record) error {
	if err := NormalizeAndValidateLayer(&record); err != nil {
		return err
	}
	if record.CreatedAt == 0 {
		record.CreatedAt = time.Now().Unix()
	}
	if record.UpdatedAt == 0 {
		record.UpdatedAt = record.CreatedAt
	}
	if p.ingestHook != nil {
		return p.ingestHook(ctx, record)
	}
	return p.store.Ingest(ctx, record)
}

func (p *Pipeline) Link(ctx context.Context, sourceID, targetID, relation string) error {
	return p.store.Link(ctx, sourceID, targetID, relation)
}

func (p *Pipeline) Recall(ctx context.Context, query string, opts RecallOptions) ([]Record, error) {
	raw, err := p.store.Recall(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	return p.rankRecords(query, raw), nil
}

// RankRecords re-scores rows with BM25-lite + importance + recency (exported for FTS fallback path).
func (p *Pipeline) RankRecords(query string, records []Record) []Record {
	return p.rankRecords(query, records)
}

func (p *Pipeline) rankRecords(query string, records []Record) []Record {
	tokens := tokenize(query)
	now := time.Now().Unix()
	type scored struct {
		r Record
		s float64
	}
	var out []scored
	for _, r := range records {
		rel := bm25LiteScore(tokens, r.Content)
		imp := r.Importance
		if imp <= 0 {
			imp = 1.0
		}
		dec := recencyDecay(r.LastRecallAt, now, 0.05)
		forgetDecay := forgettingCurveDecay(r.CreatedAt, now, 0.03)
		out = append(out, scored{r: r, s: combinedScore(rel, imp, dec*forgetDecay)})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].s > out[i].s {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	result := make([]Record, len(out))
	for i, sc := range out {
		result[i] = sc.r
	}
	return result
}

func (p *Pipeline) Feedback(ctx context.Context, id string, kind FeedbackKind) error {
	return p.store.ApplyFeedback(ctx, id, feedbackDelta(kind), kind)
}

// RunGC archives stale low-importance memories (forgetting curve).
func (p *Pipeline) RunGC(ctx context.Context, ageDays float64, importanceFloor float64) (int, error) {
	if ageDays <= 0 {
		ageDays = 30
	}
	if importanceFloor <= 0 {
		importanceFloor = 0.4
	}
	return p.store.RunGC(ctx, ageDays, importanceFloor)
}

// Distill merges recent episode memories into a reflection summary (lightweight, no LLM).
func (p *Pipeline) Distill(ctx context.Context, scope string, limit int) (string, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := p.store.ListByModality(ctx, "episode", scope, limit)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("## Distilled session summary\n")
	for _, r := range rows {
		line := strings.TrimSpace(r.Content)
		if len(line) > 200 {
			line = line[:200] + "..."
		}
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	summary := b.String()
	rec := Record{
		ID:         fmt.Sprintf("distill_%d", time.Now().Unix()),
		Content:    summary,
		Scope:      scope,
		Modality:   "reflection",
		Status:     "active",
		Importance: 1.2,
		CreatedAt:  time.Now().Unix(),
		UpdatedAt:  time.Now().Unix(),
	}
	_ = p.Ingest(ctx, rec)
	return summary, nil
}

func (p *Pipeline) ContextPrompt(ctx context.Context, sessionID string) (string, error) {
	if p.contextPromptHook != nil {
		return p.contextPromptHook(ctx, sessionID)
	}
	recs, err := p.Recall(ctx, "session context preferences", RecallOptions{Scope: sessionID, Limit: 5})
	if err != nil || len(recs) == 0 {
		recs, err = p.Recall(ctx, "user preference", RecallOptions{Scope: "global", Limit: 3})
	}
	if err != nil || len(recs) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, r := range recs {
		b.WriteString("- ")
		b.WriteString(r.Content)
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (p *Pipeline) List(ctx context.Context, limit int) ([]Record, error) {
	return p.store.ListActive(ctx, limit)
}
