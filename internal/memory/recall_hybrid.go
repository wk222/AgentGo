package memory

import (
	"context"
	"database/sql"
	"errors"
)

// recallFTS runs BM25-ranked SQLite FTS (no query embedding — fast path).
func (h *HybridEngine) recallFTS(ctx context.Context, query string, opts RecallOptions) ([]Record, error) {
	if h == nil || h.pipeline == nil {
		return nil, nil
	}
	return h.pipeline.Recall(ctx, query, opts)
}

// Recall hybrid search: parallel Milvus (eino-ext retriever) + FTS via compiled Eino Recall Graph.
func (h *HybridEngine) Recall(ctx context.Context, query string, opts RecallOptions) ([]Record, error) {
	if h == nil {
		return nil, nil
	}

	runnable, err := BuildRecallGraph(ctx)
	if err != nil {
		return nil, err
	}

	cfg := RecallConfigFromEnv()
	input := RecallInput{
		Query:  query,
		Opts:   opts,
		Config: cfg,
		Engine: h,
	}

	rows, err := runnable.Invoke(ctx, input)
	if err != nil {
		return nil, err
	}
	return h.currentRecallRecords(ctx, rows, opts.Scope)
}

// SQLite is authoritative. A vector index may still contain old status, content
// or scope after a correction; refresh every candidate before exposing it.
func (h *HybridEngine) currentRecallRecords(ctx context.Context, rows []Record, scope string) ([]Record, error) {
	var out []Record
	for _, candidate := range rows {
		current, err := h.sqlite.GetRecord(ctx, candidate.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if current.Status != "active" || current.SupersedesID != "" || current.ContradictedBy != "" {
			continue
		}
		if scope != "" && current.Scope != scope {
			continue
		}
		out = append(out, current)
	}
	return out, nil
}
