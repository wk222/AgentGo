package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool/utils"
	"github.com/google/uuid"

	"agentgo/internal/memory"
)

type rememberInput struct {
	Content    string  `json:"content" jsonschema:"description=The fact, user preference, or decision to store in long-term semantic memory"`
	Scope      string  `json:"scope" jsonschema:"description=Memory scope such as a session id, agent, or global. Defaults to global."`
	Modality   string  `json:"modality" jsonschema:"description=Memory type: fact, episode, reflection, insight, journal. Defaults to fact."`
	Importance float64 `json:"importance" jsonschema:"description=Optional importance weight (higher is recalled first). Defaults to 1.0."`
}

type rememberOutput struct {
	ID      string `json:"id,omitempty"`
	Stored  bool   `json:"stored"`
	Scope   string `json:"scope,omitempty"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

// RegisterRememberTool adds a `remember` tool so the agent can proactively write a
// fact into semantic memory within a single turn. The ingest callback routes through
// the HybridEngine/Pipeline (SQLite + embedding + optional Milvus).
func RegisterRememberTool(r *Registry, ingest func(ctx context.Context, rec memory.Record) error) error {
	if ingest == nil {
		return fmt.Errorf("remember tool: ingest function required")
	}
	t, err := utils.InferTool("remember",
		"Store an important fact, user preference, or decision into long-term semantic memory so it can be recalled in future turns. Use for durable information worth remembering across sessions.",
		func(ctx context.Context, in rememberInput) (rememberOutput, error) {
			content := strings.TrimSpace(in.Content)
			if content == "" {
				return rememberOutput{Stored: false, Error: "content required"}, nil
			}
			scope := strings.TrimSpace(in.Scope)
			if scope == "" {
				scope = "global"
			}
			modality := memory.NormalizeModality(strings.TrimSpace(in.Modality))
			if modality == "" {
				modality = "fact"
			}
			importance := in.Importance
			if importance <= 0 {
				importance = 1.0
			}
			now := time.Now().Unix()
			rec := memory.Record{
				ID:         uuid.NewString(),
				Content:    content,
				Scope:      scope,
				Modality:   modality,
				Status:     "active",
				Importance: importance,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			if err := ingest(ctx, rec); err != nil {
				return rememberOutput{Stored: false, Error: err.Error()}, nil
			}
			return rememberOutput{
				ID:      rec.ID,
				Stored:  true,
				Scope:   scope,
				Message: fmt.Sprintf("stored %s in semantic memory (scope=%s)", modality, scope),
			}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}

type recallInput struct {
	Query string `json:"query" jsonschema:"description=The query or topic to search for in long-term semantic memory"`
	Scope string `json:"scope" jsonschema:"description=Optional scope filter (e.g. session id, agent, or global). If empty, searches the current session context."`
	Limit int    `json:"limit" jsonschema:"description=Maximum number of recalled memories to return. Defaults to 5."`
}

type recallOutput struct {
	Results []memory.Record `json:"results"`
	Message string          `json:"message,omitempty"`
	Error   string          `json:"error,omitempty"`
}

// RegisterRecallTool adds a `recall_memories` tool so the agent can explicitly search semantic memory.
func RegisterRecallTool(r *Registry, recall func(ctx context.Context, query string, opts memory.RecallOptions) ([]memory.Record, error)) error {
	if recall == nil {
		return fmt.Errorf("recall tool: recall function required")
	}
	t, err := utils.InferTool("recall_memories",
		"Explicitly search long-term semantic memory for facts, preferences, or past conversation summaries matching the query. Returns a list of matching records with their modality and scope.",
		func(ctx context.Context, in recallInput) (recallOutput, error) {
			query := strings.TrimSpace(in.Query)
			if query == "" {
				return recallOutput{Error: "query required"}, nil
			}
			scope := strings.TrimSpace(in.Scope)
			limit := in.Limit
			if limit <= 0 {
				limit = 5
			}
			opts := memory.RecallOptions{
				Scope: scope,
				Limit: limit,
			}
			res, err := recall(ctx, query, opts)
			if err != nil {
				return recallOutput{Error: err.Error()}, nil
			}
			return recallOutput{Results: res}, nil
		})
	if err != nil {
		return err
	}
	r.AddTool(t)
	return nil
}
