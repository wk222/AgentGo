package agent

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/spill"
)

const (
	defaultSpillMaxBytes = 6 * 1024
	defaultSpillMaxLines = 50
	previewHeadLines     = 12
	previewTailLines     = 5
)

// SpillMiddleware intercepts large tool call outputs in the message history, offloads them to a SpillStore,
// and replaces them with a bounded preview and retrieval locator.
type SpillMiddleware struct {
	adk.TypedBaseChatModelAgentMiddleware[*schema.Message]
	store     spill.Store
	fetchTool tool.InvokableTool
	maxBytes  int
	maxLines  int
}

// NewSpillMiddleware creates a spill middleware backed by the given spill store.
func NewSpillMiddleware(store spill.Store) (*SpillMiddleware, error) {
	if store == nil {
		return nil, fmt.Errorf("spill_mw: store is required")
	}
	fetchTool, err := spill.NewFetchSpillTool(store)
	if err != nil {
		return nil, err
	}
	return &SpillMiddleware{
		store:     store,
		fetchTool: fetchTool,
		maxBytes:  defaultSpillMaxBytes,
		maxLines:  defaultSpillMaxLines,
	}, nil
}

func (m *SpillMiddleware) BeforeAgent(ctx context.Context, runCtx *adk.ChatModelAgentContext[*schema.Message]) (context.Context, *adk.ChatModelAgentContext[*schema.Message], error) {
	if runCtx == nil {
		return ctx, runCtx, nil
	}
	nCtx := *runCtx
	if m.fetchTool != nil {
		nCtx.Tools = append(nCtx.Tools, m.fetchTool)
	}
	return ctx, &nCtx, nil
}

func (m *SpillMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.TypedChatModelAgentState[*schema.Message], mc *adk.TypedModelContext[*schema.Message]) (context.Context, *adk.TypedChatModelAgentState[*schema.Message], error) {
	if state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}

	sessionID := "default"
	if sid, ok := ctx.Value("session_id").(string); ok && sid != "" {
		sessionID = sid
	}

	for _, msg := range state.Messages {
		if msg == nil || msg.Role != schema.Tool {
			continue
		}
		if len(msg.Content) < m.maxBytes && strings.Count(msg.Content, "\n") < m.maxLines {
			continue
		}

		if strings.Contains(msg.Content, "[Offloaded to spill store: spill_") {
			continue
		}

		toolCallID := msg.ToolCallID
		toolName := msg.Name
		rawBytes := []byte(msg.Content)

		record, err := m.store.Save(ctx, sessionID, toolCallID, toolName, rawBytes)
		if err != nil {
			continue
		}

		msg.Content = formatSpillPreview(msg.Content, record)
	}

	return ctx, state, nil
}

func formatSpillPreview(raw string, record *spill.SpillRecord) string {
	lines := strings.Split(raw, "\n")
	totalLines := len(lines)
	if totalLines <= previewHeadLines+previewTailLines {
		return raw
	}

	head := strings.Join(lines[:previewHeadLines], "\n")
	tail := strings.Join(lines[totalLines-previewTailLines:], "\n")

	var sb bytes.Buffer
	sb.WriteString(head)
	sb.WriteString(fmt.Sprintf("\n\n... [Offloaded to spill store: %s | %d lines, %s total] ...\n",
		record.ID, record.TotalLines, formatByteSize(record.TotalBytes)))
	sb.WriteString(fmt.Sprintf("To inspect specific sections or filter with regex, call fetch_spill_content(spill_id=%q, offset_line=..., limit_lines=...).\n\n", record.ID))
	sb.WriteString(tail)

	return sb.String()
}

func formatByteSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.2f MB", float64(bytes)/(1024*1024))
}

// BuildDefaultSpillStore initializes the default spill store rooted under dataDir.
func BuildDefaultSpillStore(dataDir string) (spill.Store, error) {
	spillDir := filepath.Join(dataDir, "spills")
	return spill.NewFileSpillStore(spillDir)
}
