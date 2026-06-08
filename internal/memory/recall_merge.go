package memory

import (
	"strings"
)

const rrfK = 60.0

// ftsMatchQuery builds an FTS5 OR query from natural language (avoids invalid MATCH syntax).
func ftsMatchQuery(query string) string {
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return `""`
	}
	var parts []string
	for _, t := range tokens {
		t = strings.Trim(t, `"'`)
		if len(t) < 2 {
			continue
		}
		parts = append(parts, `"`+strings.ReplaceAll(t, `"`, "")+`"`)
	}
	if len(parts) == 0 {
		return `"` + strings.ReplaceAll(tokens[0], `"`, "") + `"`
	}
	return strings.Join(parts, " OR ")
}

// mergeHybridRRF fuses ranked lists with weighted reciprocal rank fusion.
func mergeHybridRRF(lists [][]Record, weights []float64, limit int) []Record {
	if limit <= 0 {
		limit = 10
	}
	scores := make(map[string]float64)
	byID := make(map[string]Record)
	for li, rows := range lists {
		w := 1.0
		if li < len(weights) && weights[li] > 0 {
			w = weights[li]
		}
		for rank, r := range rows {
			if r.ID == "" {
				continue
			}
			scores[r.ID] += w * (1.0 / (rrfK + float64(rank+1)))
			if _, ok := byID[r.ID]; !ok {
				byID[r.ID] = r
			}
		}
	}
	type ranked struct {
		id string
		s  float64
	}
	var order []ranked
	for id, s := range scores {
		order = append(order, ranked{id: id, s: s})
	}
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if order[j].s > order[i].s {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	out := make([]Record, 0, limit)
	for _, rk := range order {
		if r, ok := byID[rk.id]; ok {
			out = append(out, r)
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func filterRecordsByScope(rows []Record, scope string) []Record {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return rows
	}
	var out []Record
	for _, r := range rows {
		if r.Scope == scope || r.Scope == "" {
			out = append(out, r)
		}
	}
	return out
}
