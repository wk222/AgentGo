package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentgo/internal/memory"
)

const defaultMemoryRecallQAQuery = "session context user preferences goals"

// MemoryRecallQA returns recall rows plus the exact context prompt preview used
// to reason about injection quality from the desktop UI.
func (s *AppService) MemoryRecallQA(query, scope, modality string, limit int) map[string]any {
	if s.rt.Memory() == nil {
		return map[string]any{"success": false, "error": "memory unavailable"}
	}
	query = strings.TrimSpace(query)
	if query == "" {
		query = defaultMemoryRecallQAQuery
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	modality = strings.TrimSpace(modality)
	if limit <= 0 {
		limit = 8
	}
	if limit > 30 {
		limit = 30
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	recs, err := s.rt.Memory().Recall(ctx, query, memory.RecallOptions{
		Scope:    scope,
		Modality: modality,
		Limit:    limit,
	})
	elapsedMS := time.Since(start).Milliseconds()
	if err != nil {
		return map[string]any{
			"success":    false,
			"error":      err.Error(),
			"query":      query,
			"scope":      scope,
			"modality":   modality,
			"elapsed_ms": elapsedMS,
		}
	}

	prompt, promptErr := s.rt.Memory().ContextPrompt(ctx, scope)
	rows := make([]map[string]any, 0, len(recs))
	for i, rec := range recs {
		rows = append(rows, memoryRecallQARow(query, rec, i))
	}
	out := map[string]any{
		"success":    true,
		"query":      query,
		"scope":      scope,
		"modality":   modality,
		"limit":      limit,
		"elapsed_ms": elapsedMS,
		"count":      len(rows),
		"records":    rows,
		"engine":     fmt.Sprintf("%T", s.rt.Memory()),
	}
	if promptErr != nil {
		out["prompt_error"] = promptErr.Error()
	} else {
		out["prompt"] = prompt
		out["prompt_bytes"] = len(prompt)
	}
	return out
}

func memoryRecallQARow(query string, rec memory.Record, rank int) map[string]any {
	score := memoryRecallApproxScore(rec, rank)
	return map[string]any{
		"rank":            rank + 1,
		"estimated_score": score,
		"source":          "hybrid_recall",
		"why":             memoryRecallWhy(query, rec, rank),
		"id":              rec.ID,
		"content":         rec.Content,
		"scope":           rec.Scope,
		"modality":        rec.Modality,
		"status":          rec.Status,
		"importance":      rec.Importance,
		"recall_count":    rec.RecallCount,
		"is_canonical":    rec.IsCanonical,
		"source_trust":    rec.SourceTrust,
		"supersedes_id":   rec.SupersedesID,
		"contradicted_by": rec.ContradictedBy,
		"last_recall_at":  rec.LastRecallAt,
		"created_at":      rec.CreatedAt,
		"updated_at":      rec.UpdatedAt,
		"metadata":        rec.Metadata,
	}
}

func memoryRecallApproxScore(rec memory.Record, rank int) float64 {
	importance := rec.Importance
	if importance <= 0 {
		importance = 1
	}
	if importance > 2 {
		importance = 2
	}
	score := (1.0 / float64(rank+1) * 0.7) + (importance / 2.0 * 0.3)
	return float64(int(score*1000)) / 1000
}

func memoryRecallWhy(query string, rec memory.Record, rank int) string {
	overlap := memoryTokenOverlap(query, rec.Content)
	parts := []string{fmt.Sprintf("rank #%d", rank+1)}
	if overlap > 0 {
		parts = append(parts, fmt.Sprintf("%d query token hits", overlap))
	}
	if rec.Scope != "" {
		parts = append(parts, "scope="+rec.Scope)
	}
	if rec.Modality != "" {
		parts = append(parts, "modality="+rec.Modality)
	}
	if rec.Importance > 0 {
		parts = append(parts, fmt.Sprintf("importance=%.2f", rec.Importance))
	}
	return strings.Join(parts, "; ")
}

func memoryTokenOverlap(query, content string) int {
	q := make(map[string]bool)
	for _, t := range strings.Fields(strings.ToLower(query)) {
		t = strings.Trim(t, ".,;:!?()[]{}\"'")
		if len(t) >= 2 {
			q[t] = true
		}
	}
	if len(q) == 0 {
		return 0
	}
	n := 0
	seen := make(map[string]bool)
	for _, t := range strings.Fields(strings.ToLower(content)) {
		t = strings.Trim(t, ".,;:!?()[]{}\"'")
		if q[t] && !seen[t] {
			seen[t] = true
			n++
		}
	}
	return n
}
