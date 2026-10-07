package ledger_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"agentgo/internal/ledger"
)

func TestCalculateCost(t *testing.T) {
	// 1M prompt, 1M completion for deepseek-chat
	usd, cny := ledger.CalculateCost("deepseek-chat", 1_000_000, 1_000_000, 0, 0, 0)
	assert.InDelta(t, 0.42, usd, 0.001)
	assert.InDelta(t, 0.42*7.25, cny, 0.01)

	// Prompt cache read discount
	usdCache, _ := ledger.CalculateCost("deepseek-chat", 1_000_000, 0, 0, 800_000, 0)
	// (200k * 0.14) + (800k * 0.014) = 0.028 + 0.0112 = 0.0392
	assert.InDelta(t, 0.0392, usdCache, 0.001)
}

func TestSQLiteLedgerStore(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	store, err := ledger.NewSQLiteLedgerStore(db)
	require.NoError(t, err)

	ctx := context.Background()

	// 1. Record two usage logs
	err = store.Record(ctx, &ledger.UsageRecord{
		SessionID:        "sess_001",
		LaneName:         "main",
		Model:            "deepseek-chat",
		PromptTokens:     1000,
		CompletionTokens: 500,
		DurationMS:       450,
		ToolCallsCount:   2,
	})
	require.NoError(t, err)

	err = store.Record(ctx, &ledger.UsageRecord{
		SessionID:        "sess_001",
		LaneName:         "experiment",
		Model:            "deepseek-reasoner",
		PromptTokens:     2000,
		CompletionTokens: 1000,
		ReasoningTokens:  800,
		DurationMS:       1200,
		ToolCallsCount:   0,
	})
	require.NoError(t, err)

	// 2. Query summary
	summary, err := store.GetSessionSummary(ctx, "sess_001")
	require.NoError(t, err)
	assert.Equal(t, 2, summary.TotalRequests)
	assert.Equal(t, 3000, summary.TotalPromptTokens)
	assert.Equal(t, 1500, summary.TotalCompletionTokens)
	assert.Equal(t, 4500, summary.TotalTokens)
	assert.Greater(t, summary.TotalCostUSD, 0.0)
	assert.Greater(t, summary.TotalCostCNY, 0.0)
	assert.Contains(t, summary.ModelBreakdown, "deepseek-chat")
	assert.Contains(t, summary.ModelBreakdown, "deepseek-reasoner")

	// 3. List records
	records, err := store.ListRecords(ctx, "sess_001", 10)
	require.NoError(t, err)
	assert.Len(t, records, 2)
	assert.WithinDuration(t, time.Now().UTC(), records[0].CreatedAt, 5*time.Second)
}
