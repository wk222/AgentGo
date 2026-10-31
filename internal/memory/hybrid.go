package memory

import (
	"context"
	"fmt"
	"log"
	"strings"

	"agentgo/internal/config"
)

// HybridEngine combines SQLite FTS + optional embedding rerank + optional Milvus vector store.
type HybridEngine struct {
	sqlite   *SQLiteStore
	pipeline *Pipeline
	milvus   *MilvusStore
	embedder *Embedder
	milvusOn bool
	apiBase  string
	apiKey   string
}

// BootConfig wires memory from desktop LLM settings and environment.
type BootConfig struct {
	APIBase          string
	APIKey           string
	EmbeddingModel   string
	MilvusAddr       string
	MilvusCollection string
	VectorDim        int
}

func BootConfigFromEnv(llmAPIBase, llmAPIKey string) BootConfig {
	cfg := BootConfig{
		APIBase:          llmAPIBase,
		APIKey:           llmAPIKey,
		EmbeddingModel:   config.EmbeddingModel.Get(),
		MilvusAddr:       config.MilvusAddr.Get(),
		MilvusCollection: config.MilvusColl.Get(),
		VectorDim:        embeddingDim(),
	}
	return cfg
}

// embeddingDim is the configured vector dimension; non-positive values fall back to 1536.
func embeddingDim() int {
	if n := config.EmbeddingDim.Get(); n > 0 {
		return n
	}
	return 1536
}

// NewHybridEngine builds the full memory stack (always SQLite; Milvus when addr is set and reachable).
func NewHybridEngine(ctx context.Context, sqlite *SQLiteStore, cfg BootConfig) (*HybridEngine, error) {
	if sqlite == nil {
		return nil, fmt.Errorf("sqlite store required")
	}
	h := &HybridEngine{
		sqlite:  sqlite,
		apiBase: cfg.APIBase,
		apiKey:  cfg.APIKey,
	}
	h.embedder = NewEmbedder(EmbeddingConfig{
		APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.EmbeddingModel,
	})
	h.pipeline = NewPipeline(sqlite)
	h.pipeline.SetIngestHook(h.ingestRecord)
	h.pipeline.SetContextPromptHook(func(ctx context.Context, sid string) (string, error) {
		return h.ContextPrompt(ctx, sid)
	})

	if cfg.MilvusAddr != "" && cfg.APIKey != "" {
		ms, err := connectMilvusStore(ctx, cfg)
		if err != nil {
			log.Printf("[memory] Milvus disabled: %v (using SQLite+embedding only)", err)
		} else if ms != nil {
			h.milvus = ms
			h.milvusOn = true
			log.Printf("[memory] Milvus connected %s collection=%s", cfg.MilvusAddr, cfg.MilvusCollection)
		}
	}
	if cfg.APIKey != "" {
		log.Printf("[memory] embedding enabled model=%s milvus=%v", cfg.EmbeddingModel, h.milvusOn)
	}
	return h, nil
}

func (h *HybridEngine) Pipeline() *Pipeline { return h.pipeline }

func (h *HybridEngine) ingestRecord(ctx context.Context, r Record) error {
	const maxChunkLen = 1200
	// Atomic statements must keep their canonical ID even when UTF-8 bytes exceed the chunk threshold.
	if len(r.Content) > maxChunkLen && r.Modality != "episode" && r.Metadata["source"] != "llm_distillation" && r.Metadata["atomic_container"] != true {
		chunks := chunkContent(r.Content, maxChunkLen, 200)
		if len(chunks) > 1 {
			for i, chunk := range chunks {
				chunkRec := r
				chunkRec.ID = fmt.Sprintf("%s_c%d", r.ID, i)
				chunkRec.Content = chunk
				if err := h.ingestSingleRecord(ctx, chunkRec); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return h.ingestSingleRecord(ctx, r)
}

func (h *HybridEngine) ingestSingleRecord(ctx context.Context, r Record) error {
	if h.embedder != nil && h.embedder.cfg.APIKey != "" && strings.TrimSpace(r.Content) != "" {
		// Chunks share the caller's Metadata map, so work on a copy: a failed
		// embedding must not leave the previous chunk's vector on this one.
		meta := make(map[string]interface{}, len(r.Metadata)+4)
		for k, v := range r.Metadata {
			meta[k] = v
		}
		clearEmbedding(meta)
		if vec, err := h.embedder.Embed(ctx, r.Content); err == nil {
			stampEmbedding(meta, vec, h.embedder.Space(), r.Content)
		}
		r.Metadata = meta
	}
	if err := h.sqlite.Ingest(ctx, r); err != nil {
		return err
	}
	if h.milvus != nil {
		if err := h.milvus.Ingest(ctx, r); err != nil {
			log.Printf("[memory] milvus ingest %s: %v", r.ID, err)
		}
	}
	return nil
}

func (h *HybridEngine) Ingest(ctx context.Context, record Record) error {
	return h.ingestRecord(ctx, record)
}

func (h *HybridEngine) Link(ctx context.Context, sourceID, targetID, relation string) error {
	return h.sqlite.Link(ctx, sourceID, targetID, relation)
}

func (h *HybridEngine) Feedback(ctx context.Context, id string, kind FeedbackKind) error {
	return h.sqlite.ApplyFeedback(ctx, id, feedbackDelta(kind), kind)
}

func (h *HybridEngine) ContextPrompt(ctx context.Context, sessionID string) (string, error) {
	query := RecallQueryFromContext(ctx)
	if query == "" {
		query = "session context user preferences goals"
	}
	ctx = WithRecallQuery(ctx, query)
	cfg := RecallConfigFromEnv()
	rctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	recs, err := collectContextRecords(rctx, h.Recall, query, sessionID)
	if err != nil {
		return "", err
	}
	return h.pipeline.forest.ContextPrompt(ctx, sessionID, recs, PromptTokenBudget(ctx)), nil
}
