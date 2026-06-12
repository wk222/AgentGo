package memory

import (
	"context"

	"github.com/cloudwego/eino/components/embedding"
)

// EinoEmbedder adapts our OpenAI-compatible Embedder to eino embedding.Embedder (for Milvus2).
type EinoEmbedder struct {
	inner *Embedder
}

func NewEinoEmbedder(cfg EmbeddingConfig) *EinoEmbedder {
	if cfg.APIKey == "" {
		return nil
	}
	return &EinoEmbedder{inner: NewEmbedder(cfg)}
}

func (e *EinoEmbedder) EmbedStrings(ctx context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	if e == nil || e.inner == nil {
		return nil, nil
	}
	out := make([][]float64, 0, len(texts))
	for _, t := range texts {
		v, err := e.inner.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		row := make([]float64, len(v))
		for i, f := range v {
			row[i] = float64(f)
		}
		out = append(out, row)
	}
	return out, nil
}
