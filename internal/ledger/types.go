package ledger

import (
	"context"
	"time"
)

// UsageRecord represents an immutable token consumption and cost log entry.
type UsageRecord struct {
	ID               string    `json:"id"`
	SessionID        string    `json:"session_id"`
	LaneName         string    `json:"lane_name,omitempty"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	ReasoningTokens  int       `json:"reasoning_tokens,omitempty"`
	CacheReadTokens  int       `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int       `json:"cache_write_tokens,omitempty"`
	TotalTokens      int       `json:"total_tokens"`
	CostUSD          float64   `json:"cost_usd"`
	CostCNY          float64   `json:"cost_cny"`
	DurationMS       int64     `json:"duration_ms,omitempty"`
	ToolCallsCount   int       `json:"tool_calls_count,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// SessionUsageSummary aggregates token usage and costs for a session or lane.
type SessionUsageSummary struct {
	SessionID            string             `json:"session_id"`
	TotalRequests        int                `json:"total_requests"`
	TotalPromptTokens    int                `json:"total_prompt_tokens"`
	TotalCompletionTokens int               `json:"total_completion_tokens"`
	TotalReasoningTokens int                `json:"total_reasoning_tokens"`
	TotalCacheReadTokens int                `json:"total_cache_read_tokens"`
	TotalTokens          int                `json:"total_tokens"`
	TotalCostUSD         float64            `json:"total_cost_usd"`
	TotalCostCNY         float64            `json:"total_cost_cny"`
	ModelBreakdown       map[string]float64 `json:"model_breakdown"`
}

// Store persists usage ledger entries and queries aggregates.
type Store interface {
	Record(ctx context.Context, record *UsageRecord) error
	GetSessionSummary(ctx context.Context, sessionID string) (*SessionUsageSummary, error)
	ListRecords(ctx context.Context, sessionID string, limit int) ([]UsageRecord, error)
}
