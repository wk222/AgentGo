package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// SQLiteLedgerStore implements Store using SQLite.
type SQLiteLedgerStore struct {
	db *sql.DB
}

// NewSQLiteLedgerStore creates and initializes the ledger table.
func NewSQLiteLedgerStore(db *sql.DB) (*SQLiteLedgerStore, error) {
	if db == nil {
		return nil, errors.New("ledger: db is required")
	}

	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS usage_ledger (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			lane_name TEXT DEFAULT 'main',
			model TEXT NOT NULL,
			prompt_tokens INTEGER NOT NULL,
			completion_tokens INTEGER NOT NULL,
			reasoning_tokens INTEGER DEFAULT 0,
			cache_read_tokens INTEGER DEFAULT 0,
			cache_write_tokens INTEGER DEFAULT 0,
			total_tokens INTEGER NOT NULL,
			cost_usd REAL NOT NULL,
			cost_cny REAL NOT NULL,
			duration_ms INTEGER DEFAULT 0,
			tool_calls_count INTEGER DEFAULT 0,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_usage_ledger_session ON usage_ledger(session_id);
		CREATE INDEX IF NOT EXISTS idx_usage_ledger_model ON usage_ledger(model);
	`)
	if err != nil {
		return nil, err
	}

	return &SQLiteLedgerStore{db: db}, nil
}

// Record persists a single usage log entry.
func (s *SQLiteLedgerStore) Record(ctx context.Context, r *UsageRecord) error {
	if r == nil {
		return errors.New("ledger: record is nil")
	}
	if r.ID == "" {
		r.ID = fmt.Sprintf("usg_%s", uuid.NewString()[:12])
	}
	if r.LaneName == "" {
		r.LaneName = "main"
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.TotalTokens == 0 {
		r.TotalTokens = r.PromptTokens + r.CompletionTokens
	}
	if r.CostUSD == 0 && r.CostCNY == 0 {
		r.CostUSD, r.CostCNY = CalculateCost(r.Model, r.PromptTokens, r.CompletionTokens, r.ReasoningTokens, r.CacheReadTokens, r.CacheWriteTokens)
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_ledger (
			id, session_id, lane_name, model,
			prompt_tokens, completion_tokens, reasoning_tokens,
			cache_read_tokens, cache_write_tokens, total_tokens,
			cost_usd, cost_cny, duration_ms, tool_calls_count, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, r.ID, r.SessionID, r.LaneName, r.Model,
		r.PromptTokens, r.CompletionTokens, r.ReasoningTokens,
		r.CacheReadTokens, r.CacheWriteTokens, r.TotalTokens,
		r.CostUSD, r.CostCNY, r.DurationMS, r.ToolCallsCount, r.CreatedAt.Unix())

	return err
}

// GetSessionSummary computes aggregated statistics for a session.
func (s *SQLiteLedgerStore) GetSessionSummary(ctx context.Context, sessionID string) (*SessionUsageSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT model,
			COUNT(*),
			COALESCE(SUM(prompt_tokens), 0),
			COALESCE(SUM(completion_tokens), 0),
			COALESCE(SUM(reasoning_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(total_tokens), 0),
			COALESCE(SUM(cost_usd), 0),
			COALESCE(SUM(cost_cny), 0)
		FROM usage_ledger
		WHERE session_id = ?
		GROUP BY model
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summary := &SessionUsageSummary{
		SessionID:      sessionID,
		ModelBreakdown: make(map[string]float64),
	}

	for rows.Next() {
		var model string
		var reqs, pTokens, cTokens, rTokens, crTokens, tTokens int
		var costUSD, costCNY float64

		if err := rows.Scan(&model, &reqs, &pTokens, &cTokens, &rTokens, &crTokens, &tTokens, &costUSD, &costCNY); err != nil {
			return nil, err
		}

		summary.TotalRequests += reqs
		summary.TotalPromptTokens += pTokens
		summary.TotalCompletionTokens += cTokens
		summary.TotalReasoningTokens += rTokens
		summary.TotalCacheReadTokens += crTokens
		summary.TotalTokens += tTokens
		summary.TotalCostUSD += costUSD
		summary.TotalCostCNY += costCNY
		summary.ModelBreakdown[model] = costUSD
	}

	return summary, nil
}

// ListRecords returns recent usage records for a session.
func (s *SQLiteLedgerStore) ListRecords(ctx context.Context, sessionID string, limit int) ([]UsageRecord, error) {
	if limit <= 0 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, lane_name, model,
			prompt_tokens, completion_tokens, reasoning_tokens,
			cache_read_tokens, cache_write_tokens, total_tokens,
			cost_usd, cost_cny, duration_ms, tool_calls_count, created_at
		FROM usage_ledger
		WHERE session_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []UsageRecord
	for rows.Next() {
		var r UsageRecord
		var createdAtUnix int64
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.LaneName, &r.Model,
			&r.PromptTokens, &r.CompletionTokens, &r.ReasoningTokens,
			&r.CacheReadTokens, &r.CacheWriteTokens, &r.TotalTokens,
			&r.CostUSD, &r.CostCNY, &r.DurationMS, &r.ToolCallsCount, &createdAtUnix,
		); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(createdAtUnix, 0).UTC()
		records = append(records, r)
	}

	return records, nil
}
