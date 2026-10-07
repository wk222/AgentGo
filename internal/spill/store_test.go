package spill

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileSpillStore_SaveAndFetch(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewFileSpillStore(tempDir)
	require.NoError(t, err)

	ctx := context.Background()

	// 1. Generate multi-line content
	var sb strings.Builder
	for i := 1; i <= 200; i++ {
		sb.WriteString(fmt.Sprintf("log line %d: user action %d performed\n", i, i))
	}
	raw := []byte(sb.String())

	// 2. Save
	record, err := store.Save(ctx, "session_123", "call_abc", "execute_bash", raw)
	require.NoError(t, err)
	require.NotNil(t, record)
	assert.Equal(t, 200, record.TotalLines)
	assert.True(t, strings.HasPrefix(record.ID, "spill_"))

	// 3. Fetch slice
	res, err := store.Fetch(ctx, record.ID, FetchOptions{
		OffsetLine: 10,
		LimitLines: 5,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 5, res.ReturnedLines)
	assert.True(t, res.HasMore)
	assert.Contains(t, res.Content, "log line 10")
	assert.Contains(t, res.Content, "log line 14")
	assert.NotContains(t, res.Content, "log line 15")

	// 4. Fetch with regex filter
	resFiltered, err := store.Fetch(ctx, record.ID, FetchOptions{
		OffsetLine:  1,
		LimitLines:  10,
		RegexFilter: `log line 1[0-5]:`,
	})
	require.NoError(t, err)
	require.NotNil(t, resFiltered)
	assert.Equal(t, 6, resFiltered.ReturnedLines)

	// 5. Test Tool
	tool, err := NewFetchSpillTool(store)
	require.NoError(t, err)
	require.NotNil(t, tool)
}

func TestFileSpillStore_Cleanup(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewFileSpillStore(tempDir)
	require.NoError(t, err)

	ctx := context.Background()
	rec, err := store.Save(ctx, "sess_x", "call_1", "tool_a", []byte("test content\nline 2"))
	require.NoError(t, err)

	err = store.CleanupSession(ctx, "sess_x")
	require.NoError(t, err)

	_, err = store.GetRecord(ctx, rec.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}
