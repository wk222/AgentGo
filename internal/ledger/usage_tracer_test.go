package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestModelUsageTracer(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "agentgo_usage_test_"+time.Now().Format("150405"))
	defer os.RemoveAll(tmpDir)

	tracer := NewModelUsageTracer(tmpDir, 10*time.Second)
	defer tracer.Close()

	// 1. Masking
	assert.Equal(t, "sk-123***7890", MaskAPIKey("sk-1234567890"))
	assert.Equal(t, "***", MaskAPIKey("short"))

	// 2. Record calls
	tracer.Record("gpt-4o", "sk-1234567890", 100, 30, 50, 20, 500*time.Millisecond)
	tracer.Record("gpt-4o", "sk-1234567890", 200, 50, 80, 40, 700*time.Millisecond)

	// 3. Get summary
	today := time.Now().Format("2006-01-02")
	summary, err := tracer.GetDailySummary(today)
	assert.NoError(t, err)

	hashKey := "gpt-4o|sk-123***7890"
	entry, ok := summary[hashKey]
	assert.True(t, ok)
	assert.Equal(t, 2, entry.Calls)
	assert.Equal(t, 300, entry.PromptTokens)
	assert.Equal(t, 80, entry.CachedTokens)
	assert.Equal(t, 130, entry.CompletionTokens)
	assert.Equal(t, 60, entry.ReasoningTokens)
	assert.Equal(t, 430, entry.TotalTokens)
}
