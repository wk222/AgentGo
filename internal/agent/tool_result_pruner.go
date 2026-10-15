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
	// Aligned with SessionSpine cache-first window (6 turns) to protect prefix KV cache.
	defaultKeepRecentToolTurns = 6
	minPrunableContentLen      = 1200
	minPrunableLines           = 12
	prunedPreviewChars         = 640
)

// isLikelyToolErrorOutput checks if a tool output contains compiler/runtime failure signals
// that should never have their tail truncated.
func isLikelyToolErrorOutput(content string) bool {
	if strings.HasPrefix(content, "[tool error]") {
		return true
	}
	lower := strings.ToLower(content)
	return strings.Contains(lower, "exit status ") ||
		strings.Contains(lower, "panic:") ||
		strings.Contains(lower, "syntaxerror") ||
		strings.Contains(lower, "compilation failed")
}

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

		if strings.HasPrefix(msg.Content, "[Pruned previous output:") ||
			strings.HasPrefix(msg.Content, "[Offloaded to spill store:") ||
			isLikelyToolErrorOutput(msg.Content) {
			continue
		}

		lineCount := strings.Count(msg.Content, "\n") + 1
		if lineCount < minPrunableLines {
			continue
		}

		headLen := (prunedPreviewChars * 5) / 8
		tailLen := prunedPreviewChars - headLen
		if headLen+tailLen >= len(msg.Content) {
			firstLine := strings.TrimSpace(strings.Split(msg.Content, "\n")[0])
			if len(firstLine) > 80 {
				firstLine = firstLine[:80] + "..."
			}
			msg.Content = fmt.Sprintf("[Pruned previous output: %d lines (%d bytes), initial: %q. Superseded by newer turn actions]",
				lineCount, len(msg.Content), firstLine)
			continue
		}

		head := strings.TrimSpace(msg.Content[:headLen])
		tail := strings.TrimSpace(msg.Content[len(msg.Content)-tailLen:])
		omitted := len(msg.Content) - headLen - tailLen
		msg.Content = fmt.Sprintf(
			"[Pruned previous output: %d lines (%d bytes), omitted %d middle bytes]\n%s\n... [middle omitted] ...\n%s",
			lineCount, len(msg.Content), omitted, head, tail,
		)
	}

	return ctx, state, nil
}
