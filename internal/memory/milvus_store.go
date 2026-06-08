package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	milvusindexer "github.com/cloudwego/eino-ext/components/indexer/milvus2"
	milvusretriever "github.com/cloudwego/eino-ext/components/retriever/milvus2"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// MilvusStore implements a memory store using Milvus as the backend
// via eino-ext milvus2 retriever and indexer.
type MilvusStore struct {
	indexer   *milvusindexer.Indexer
	retriever *milvusretriever.Retriever
}

// NewMilvusStore initializes a Milvus-backed memory store.
func NewMilvusStore(ctx context.Context, cfg *milvusindexer.IndexerConfig, retCfg *milvusretriever.RetrieverConfig) (*MilvusStore, error) {
	// Initialize Indexer
	idx, err := milvusindexer.NewIndexer(ctx, cfg)
	if err != nil {
		return nil, err
	}

	// Initialize Retriever
	ret, err := milvusretriever.NewRetriever(ctx, retCfg)
	if err != nil {
		return nil, err
	}

	return &MilvusStore{
		indexer:   idx,
		retriever: ret,
	}, nil
}

func (s *MilvusStore) Ingest(ctx context.Context, record Record) error {
	metadata := map[string]any{
		"id":         record.ID,
		"scope":      record.Scope,
		"modality":   record.Modality,
		"status":     record.Status,
		"created_at": record.CreatedAt,
		"updated_at": record.UpdatedAt,
	}
	
	for k, v := range record.Metadata {
		metadata[k] = v
	}

	doc := &schema.Document{
		ID:       record.ID,
		Content:  record.Content,
		MetaData: metadata,
	}

	_, err := s.indexer.Store(ctx, []*schema.Document{doc})
	return err
}

func (s *MilvusStore) Recall(ctx context.Context, query string, opts RecallOptions) ([]Record, error) {
	topK := opts.Limit
	if topK <= 0 {
		topK = 10
	}
	if topK < 12 {
		topK = 12
	}
	retOpts := []retriever.Option{
		retriever.WithTopK(topK),
	}
	var filters []string
	if scope := strings.TrimSpace(opts.Scope); scope != "" {
		filters = append(filters, fmt.Sprintf(`scope == %q`, scope))
	}
	if opts.StartTime > 0 {
		filters = append(filters, fmt.Sprintf(`created_at >= %d`, opts.StartTime))
	}
	if opts.Modality != "" {
		filters = append(filters, fmt.Sprintf(`modality == %q`, opts.Modality))
	}
	if len(filters) > 0 {
		retOpts = append(retOpts, milvusretriever.WithFilter(strings.Join(filters, " && ")))
	}

	docs, err := s.retriever.Retrieve(ctx, query, retOpts...)
	if err != nil {
		return nil, err
	}

	var results []Record
	for _, doc := range docs {
		r := Record{
			ID:       doc.ID,
			Content:  doc.Content,
			Metadata: make(map[string]any),
		}

		if val, ok := doc.MetaData["scope"].(string); ok {
			r.Scope = val
		}
		if val, ok := doc.MetaData["modality"].(string); ok {
			r.Modality = val
		}
		if val, ok := doc.MetaData["status"].(string); ok {
			r.Status = val
		}
		
		results = append(results, r)
	}

	return results, nil
}

func (s *MilvusStore) Feedback(ctx context.Context, id string, kind FeedbackKind) error {
	return nil
}

func (s *MilvusStore) ContextPrompt(ctx context.Context, sessionID string) (string, error) {
	opts := RecallOptions{
		Scope: "session",
		Limit: 5,
	}
	records, err := s.Recall(ctx, "session context", opts)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(records)
	return string(b), nil
}
