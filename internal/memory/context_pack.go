package memory

import (
	"agentgo/internal/promptbudget"
	"context"
	"errors"
	"sort"
	"strings"
)

// packContextRecords is shared by all context producers, including enrichment.
// Ranking is supplied by retrieval; deduplication prefers the newer record.
func packContextRecords(records []Record, budget int) string {
	if budget <= 0 {
		return ""
	}
	byText := map[string]Record{}
	keys := []string{}
	for _, r := range records {
		if (r.Status != "" && r.Status != "active") || r.SupersedesID != "" || r.ContradictedBy != "" {
			continue
		}
		if r.Metadata["atomic_children"] == true || r.Metadata["atomic_container"] == true {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(r.Content), " "))
		if key == "" {
			continue
		}
		old, exists := byText[key]
		if !exists {
			keys = append(keys, key)
		}
		if !exists || r.UpdatedAt > old.UpdatedAt {
			byText[key] = r
		}
	}
	// Preserve retrieval order. Equal content is one atom regardless of source ID.
	var b strings.Builder
	for _, key := range keys {
		r := byText[key]
		r.Content = promptbudget.Excerpt(r.Content, 180)
		line := formatRecallRecord(r, 0) + "\n"
		if promptbudget.Estimate(b.String()+line) > budget {
			continue
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}

func collectContextRecords(ctx context.Context, recall func(context.Context, string, RecallOptions) ([]Record, error), query, scope string) ([]Record, error) {
	rows, err := recall(ctx, query, RecallOptions{Scope: scope, Limit: 12})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if scope == "global" {
		return rows, err
	}
	global, globalErr := recall(ctx, query, RecallOptions{Scope: "global", Limit: 4})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && globalErr != nil {
		return nil, errors.Join(err, globalErr)
	}
	if len(rows) == 0 && len(global) == 0 {
		return nil, errors.Join(err, globalErr)
	}
	// Reserve room for relevant global constraints even when the session has matches.
	if len(rows) > 8 {
		rows = rows[:8]
	}
	rows = append(rows, global...)
	// A small priority boost for explicit canonical memories, without promoting LLM summaries.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].IsCanonical && !rows[j].IsCanonical })
	return rows, nil
}
