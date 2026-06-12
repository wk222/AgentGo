package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// EmbeddingConfig points at an OpenAI-compatible /embeddings endpoint.
type EmbeddingConfig struct {
	APIBase string
	APIKey  string
	Model   string
}

// Embedder computes dense vectors; falls back to nil (BM25-only recall).
type Embedder struct {
	cfg EmbeddingConfig
}

func NewEmbedder(cfg EmbeddingConfig) *Embedder {
	if cfg.Model == "" {
		cfg.Model = "text-embedding-3-small"
	}
	return &Embedder{cfg: cfg}
}

func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e.cfg.APIKey == "" || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embeddings unavailable")
	}
	base := strings.TrimRight(e.cfg.APIBase, "/")
	body, _ := json.Marshal(map[string]any{
		"model": e.cfg.Model,
		"input": text,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("embeddings http %d", resp.StatusCode)
	}
	var parsed struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &parsed) != nil || len(parsed.Data) == 0 {
		return nil, fmt.Errorf("invalid embeddings response")
	}
	out := make([]float32, len(parsed.Data[0].Embedding))
	for i, v := range parsed.Data[0].Embedding {
		out[i] = float32(v)
	}
	return out, nil
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// RecallWithEmbedding blends FTS candidates with embedding similarity when available.
func (p *Pipeline) RecallWithEmbedding(ctx context.Context, query string, opts RecallOptions, emb *Embedder) ([]Record, error) {
	rows, err := p.Recall(ctx, query, opts)
	if err != nil || emb == nil {
		return rows, err
	}
	qv, err := emb.Embed(ctx, query)
	if err != nil {
		return rows, nil
	}
	type scored struct {
		r Record
		s float64
	}
	var out []scored
	now := time.Now().Unix()
	for _, r := range rows {
		vec, ok := r.Metadata["embedding"].([]float32)
		if !ok {
			if arr, ok2 := r.Metadata["embedding"].([]interface{}); ok2 && len(arr) > 0 {
				vec = make([]float32, len(arr))
				for i, v := range arr {
					if f, ok := v.(float64); ok {
						vec[i] = float32(f)
					}
				}
			}
		}
		rel := cosineSimilarity(qv, vec)
		if rel <= 0 {
			continue
		}
		dec := recencyDecay(r.LastRecallAt, now, 0.05)
		forgetDecay := forgettingCurveDecay(r.CreatedAt, now, 0.03)
		out = append(out, scored{r: r, s: rel * r.Importance * dec * forgetDecay})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].s > out[i].s {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) == 0 {
		return rows, nil
	}
	result := make([]Record, len(out))
	for i, sc := range out {
		result[i] = sc.r
	}
	return result, nil
}
