package memory

import (
	"context"
	"fmt"
	"log"
	"strings"

	milvusindexer "github.com/cloudwego/eino-ext/components/indexer/milvus2"
	milvusretriever "github.com/cloudwego/eino-ext/components/retriever/milvus2"
	"github.com/cloudwego/eino-ext/components/retriever/milvus2/search_mode"
	"github.com/milvus-io/milvus/client/v2/milvusclient"

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
		einoEmb := NewEinoEmbedder(EmbeddingConfig{
			APIBase: cfg.APIBase, APIKey: cfg.APIKey, Model: cfg.EmbeddingModel,
		})
		if einoEmb != nil {
			clientCfg := &milvusclient.ClientConfig{Address: cfg.MilvusAddr}
			idxCfg := &milvusindexer.IndexerConfig{
				ClientConfig: clientCfg,
				Collection:   cfg.MilvusCollection,
				Embedding:    einoEmb,
				Vector:       &milvusindexer.VectorConfig{Dimension: int64(cfg.VectorDim)},
			}
			retCfg := &milvusretriever.RetrieverConfig{
				ClientConfig: clientCfg,
				Collection:   cfg.MilvusCollection,
				Embedding:    einoEmb,
				TopK:         10,
				SearchMode:   search_mode.NewApproximate(milvusretriever.COSINE),
			}
			ms, err := NewMilvusStore(ctx, idxCfg, retCfg)
			if err != nil {
				log.Printf("[memory] Milvus disabled: %v (using SQLite+embedding only)", err)
			} else {
				h.milvus = ms
				h.milvusOn = true
				log.Printf("[memory] Milvus connected %s collection=%s", cfg.MilvusAddr, cfg.MilvusCollection)
			}
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
	if len(r.Content) > maxChunkLen && r.Modality != "episode" {
		chunks := ChunkContent(r.Content, maxChunkLen, 200)
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
		if vec, err := h.embedder.Embed(ctx, r.Content); err == nil {
			if r.Metadata == nil {
				r.Metadata = map[string]interface{}{}
			}
			r.Metadata["embedding"] = vec
		}
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
	cfg := RecallConfigFromEnv()
	rctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	recs, err := h.Recall(rctx, "session context user preferences goals", RecallOptions{
		Scope: sessionID, Limit: 8,
	})
	if err != nil || len(recs) == 0 {
		recs, _ = h.Recall(rctx, "global preferences", RecallOptions{Scope: "global", Limit: 4})
	}
	if len(recs) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("## Recalled memories (FTS + Milvus hybrid)\n")
	for _, r := range recs {
		line := strings.TrimSpace(r.Content)
		if len(line) > 300 {
			line = line[:300] + "..."
		}
		b.WriteString("- [")
		b.WriteString(r.Modality)
		b.WriteString("] ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String(), nil
}
