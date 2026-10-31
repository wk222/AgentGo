package agent

import (
	"agentgo/internal/promptbudget"
	"encoding/json"
	"github.com/cloudwego/eino/schema"
	"strings"
)

func memoryReadPositions(messages []*schema.Message) map[int]bool {
	names := map[string]string{}
	for _, m := range messages {
		if m != nil {
			for _, c := range m.ToolCalls {
				names[c.ID] = c.Function.Name
			}
		}
	}
	positions := map[int]bool{}
	for i, m := range messages {
		if m != nil && m.Role == schema.Tool && (m.Name == "browse_memory" || names[m.ToolCallID] == "browse_memory") {
			positions[i] = true
		}
	}
	return positions
}

// Fold older read payloads under the RESIDENT budget. Original tool/result
// pairing and archive objects remain intact; the summary tree is the coarse view.
func foldMemoryReadMessages(messages []*schema.Message, budget int) ([]*schema.Message, bool) {
	positions := memoryReadPositions(messages)
	out := append([]*schema.Message(nil), messages...)
	changed := false
	remaining := max(0, budget)
	for i := len(messages) - 1; i >= 0; i-- {
		if !positions[i] {
			continue
		}
		m := messages[i]
		cost := promptbudget.Estimate(m.Content)
		if cost <= remaining {
			remaining -= cost
			continue
		}
		folded := ""
		var result memoryBrowseResult
		if json.Unmarshal([]byte(m.Content), &result) == nil {
			// Keep a reopening locator when possible; all original records stay in the tree.
			ids := []string{}
			for _, item := range result.Items {
				if len(ids) == 2 {
					break
				}
				id := item.RecordID
				if id == "" {
					id = item.ID
				}
				if id != "" {
					ids = append(ids, id)
				}
			}
			candidate := "[Memory folded; reopen " + strings.Join(ids, ",") + "]"
			if promptbudget.Estimate(candidate) <= remaining {
				folded = candidate
			}
		}
		copy := *m
		copy.Content = folded
		out[i] = &copy
		remaining -= promptbudget.Estimate(folded)
		changed = true
	}
	return out, changed
}
func (b *memoryReadBudget) beginModelProjection() {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Parallel tool calls reserve a single pending batch. Model projection folds
	// old reads, releasing their slots before the next batch is requested.
	b.toolTokens = 0
	b.automatic = 0
}
