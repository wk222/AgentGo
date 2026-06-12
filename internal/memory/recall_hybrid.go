package memory

import (
	"context"
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

	return runnable.Invoke(ctx, input)
}
