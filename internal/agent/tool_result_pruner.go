package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

const (
	// KeepRecentToolTurns specifies how many recent tool responses to keep unpruned.
	defaultKeepRecentToolTurns = 2
	minPrunableContentLen      = 400
	minPrunableLines           = 8
)

// ToolResultPrunerMiddleware performs rule-based context compaction on older tool outputs
// without invoking any LLM, reducing token waste while preserving conversational flow.
type ToolResultPrunerMiddleware struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	keepRecent int
}

// NewToolResultPrunerMiddleware creates a static tool result pruning middleware.
func NewToolResultPrunerMiddleware(keepRecent int) *ToolResultPrunerMiddleware {
	if keepRecent <= 0 {
		keepRecent = defaultKeepRecentToolTurns
	}
	return &ToolResultPrunerMiddleware{
		keepRecent: keepRecent,
	}
}

func (m *ToolResultPrunerMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.Message], mc *adk.TypedModelContext[*schema.Message]) (context.Context, *adk.TypedChatModelAgentState[*schema.Message], error) {
	if state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}

	msgs := state.Messages
	var toolIndices []int
	for i, msg := range msgs {
		if msg != nil && msg.Role == schema.Tool {
			toolIndices = append(toolIndices, i)
		}
	}

	if len(toolIndices) <= m.keepRecent {
		return ctx, state, nil
	}

	pruneThresholdIndex := len(toolIndices) - m.keepRecent
	for k := 0; k < pruneThresholdIndex; k++ {
		idx := toolIndices[k]
		msg := msgs[idx]
		if msg == nil || len(msg.Content) < minPrunableContentLen {
			continue
		}

		lineCount := strings.Count(msg.Content, "\n") + 1
		if lineCount < minPrunableLines {
			continue
		}

		if strings.HasPrefix(msg.Content, "[Pruned previous output:") {
			continue
		}

		firstLine := strings.TrimSpace(strings.Split(msg.Content, "\n")[0])
		if len(firstLine) > 80 {
			firstLine = firstLine[:80] + "..."
		}

		msg.Content = fmt.Sprintf("[Pruned previous output: %d lines (%d bytes), initial: %q. Superseded by newer turn actions]",
			lineCount, len(msg.Content), firstLine)
	}

	return ctx, state, nil
}
