package memory

import (
	"context"
)

type Record struct {
	ID             string                 `json:"id"`
	Content        string                 `json:"content"`
	Scope          string                 `json:"scope"`    // e.g., session, agent, admin, global
	Modality       string                 `json:"modality"` // fact, episode, reflection, insight, journal
	Metadata       map[string]interface{} `json:"metadata"`
	Status         string                 `json:"status"` // active, forgotten, archived
	Importance     float64                `json:"importance"`
	LastRecallAt   int64                  `json:"last_recall_at"`
	RecallCount    int                    `json:"recall_count"`
	SupersedesID   string                 `json:"supersedes_id,omitempty"`
	IsCanonical    bool                   `json:"is_canonical"`
	SourceTrust    float64                `json:"source_trust"`
	ContradictedBy string                 `json:"contradicted_by,omitempty"`
	CreatedAt      int64                  `json:"created_at"`
	UpdatedAt      int64                  `json:"updated_at"`
}

// Engine defines the interface for the Semantic Memory Engine
// that ports the core capabilities from PyBot.
type Engine interface {
	// Ingest adds a new record to the memory store.
	Ingest(ctx context.Context, record Record) error

	// Link creates a Graph-Lite edge between two memory records.
	Link(ctx context.Context, sourceID, targetID, relation string) error

	// Recall searches for relevant memories based on query and filters, including 1-hop expansions.
	Recall(ctx context.Context, query string, opts RecallOptions) ([]Record, error)

	// Feedback applies positive/negative feedback to adjust memory weighting.
	Feedback(ctx context.Context, id string, kind FeedbackKind) error

	// ContextPrompt generates the LLM prompt context from recalled memories.
	ContextPrompt(ctx context.Context, sessionID string) (string, error)
}

// RecallOptions configuration for Recall operations.
type RecallOptions struct {
	Scope         string
	Limit         int
	MinScore      float64
	StartTime     int64   // Filter memories created at or after this unix timestamp
	Modality      string  // Filter specific memory modality (e.g., episode, journal, fact)
	MinImportance float64 // Filter memories with importance greater than or equal to this
}

// FeedbackKind represents the type of user/system feedback.
type FeedbackKind string

const (
	FeedbackPositive  FeedbackKind = "positive"
	FeedbackNegative  FeedbackKind = "negative"
	FeedbackDisproved FeedbackKind = "disproved"
)
