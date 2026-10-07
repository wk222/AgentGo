package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpillMiddleware_BeforeModelRewriteState(t *testing.T) {
	tempDir := t.TempDir()
	spStore, err := BuildDefaultSpillStore(tempDir)
	require.NoError(t, err)

	mw, err := NewSpillMiddleware(spStore)
	require.NoError(t, err)

	// Create long tool response (> 50 lines)
	var sb strings.Builder
	for i := 1; i <= 80; i++ {
		sb.WriteString(fmt.Sprintf("file_%03d.txt: matches query line %d\n", i, i))
	}
	largeContent := sb.String()

	state := &adk.TypedChatModelAgentState[*schema.Message]{
		Messages: []*schema.Message{
			schema.UserMessage("search for files"),
			{
				Role:       schema.Tool,
				Name:       "grep",
				ToolCallID: "call_1",
				Content:    largeContent,
			},
		},
	}

	ctx := context.Background()
	_, out, err := mw.BeforeModelRewriteState(ctx, state, nil)
	require.NoError(t, err)
	require.NotNil(t, out)

	toolMsg := out.Messages[1]
	assert.Contains(t, toolMsg.Content, "[Offloaded to spill store: spill_")
	assert.Contains(t, toolMsg.Content, "fetch_spill_content")
	assert.Contains(t, toolMsg.Content, "file_001.txt")
	assert.Contains(t, toolMsg.Content, "file_080.txt")
}

func TestToolResultPrunerMiddleware_BeforeModelRewriteState(t *testing.T) {
	pruner := NewToolResultPrunerMiddleware(1)

	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		sb.WriteString(fmt.Sprintf("initial exploratory scan result item %d\n", i))
	}
	exploratoryOutput := sb.String()

	state := &adk.TypedChatModelAgentState[*schema.Message]{
		Messages: []*schema.Message{
			schema.UserMessage("step 1: explore"),
			{
				Role:       schema.Tool,
				Name:       "list_workspace_dir",
				ToolCallID: "call_old",
				Content:    exploratoryOutput,
			},
			schema.AssistantMessage("I found the files, now executing build", nil),
			schema.UserMessage("step 2: build"),
			{
				Role:       schema.Tool,
				Name:       "execute_bash",
				ToolCallID: "call_recent",
				Content:    "Build succeeded in 1.2s",
			},
		},
	}

	ctx := context.Background()
	_, out, err := pruner.BeforeModelRewriteState(ctx, state, nil)
	require.NoError(t, err)
	require.NotNil(t, out)

	// Old tool message pruned
	assert.Contains(t, out.Messages[1].Content, "[Pruned previous output:")
	assert.NotContains(t, out.Messages[1].Content, "exploratory scan result item 20")

	// Recent tool message kept intact
	assert.Equal(t, "Build succeeded in 1.2s", out.Messages[4].Content)
}
