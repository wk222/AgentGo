package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// The memory graph for the console: how stored memories are linked to each
// other, shown as a whole (the most connected ones) or around one memory.
//
// It only reads. It shows the same memories a run in that scope could be given:
// the global ones plus the named conversation's, active only, and never the
// evidence rows kept for personal learning.

const (
	GraphModeOverview = "overview"
	GraphModeEgo      = "ego"

	graphDefaultNodes = 80
	graphMaxNodes     = 300
	graphMaxDepth     = 3
	graphMaxEdgeRows  = 200000 // bounds the read on a very large store
	graphTitleRunes   = 48
)

// ErrGraphNotFound is returned when the memory a view is centred on does not
// exist or is not visible in the scope.
var ErrGraphNotFound = errors.New("memory not found in this scope")

type GraphRequest struct {
	Scope      string   // a conversation id or "global"; a conversation also sees the global memories
	Mode       string   // GraphModeOverview (default) or GraphModeEgo
	Center     string   // memory id, required for ego
	Depth      int      // ego: how many links away, 1..3 (default 1)
	Modalities []string // empty = all kinds; the centre of an ego view is always kept
	Limit      int      // most nodes returned, 1..300 (default 80)
}

type GraphNode struct {
	ID         string  `json:"id"`
	Title      string  `json:"title"`
	Modality   string  `json:"modality"`
	Scope      string  `json:"scope"`
	Importance float64 `json:"importance"`
	Canonical  bool    `json:"canonical,omitempty"`
	Degree     int     `json:"degree"` // links to other matching visible memories, returned or not
}

type GraphEdge struct {
	Source   string  `json:"source"`
	Target   string  `json:"target"`
	Relation string  `json:"relation,omitempty"`
	Weight   float64 `json:"weight"`
}

type GraphMeta struct {
	Mode      string         `json:"mode"`
	Scope     string         `json:"scope"`
	Total     int            `json:"total"`    // linked memories that match, before the limit
	Returned  int            `json:"returned"` // nodes in this reply
	Truncated bool           `json:"truncated"`
	Isolated  int            `json:"isolated"` // matching memories with no links at all (never drawn)
	Center    string         `json:"center,omitempty"`
	Depth     int            `json:"depth,omitempty"`
	Groups    map[string]int `json:"groups"` // returned nodes per kind, for the captions
}

type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	Meta  GraphMeta   `json:"meta"`
}

func graphScopes(scope string) []string {
	scope = strings.TrimSpace(scope)
	if scope == "" || scope == "global" {
		return []string{"global"}
	}
	return []string{"global", scope}
}

func clampInt(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type graphLink struct {
	a, b     string
	relation string
	weight   float64
}

// visibleLinks reads every link whose two ends are both visible, with each
// end's kind.
func (s *SQLiteStore) visibleLinks(ctx context.Context, scopes []string) ([]graphLink, map[string]string, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(scopes)), ",")
	q := `SELECT l.source_id, l.target_id, COALESCE(l.relation,''), COALESCE(l.weight,1.0), a.modality, b.modality
		FROM memory_links l
		JOIN memories a ON a.id = l.source_id
		JOIN memories b ON b.id = l.target_id
		WHERE a.status='active' AND b.status='active'
		  AND a.scope IN (` + ph + `) AND b.scope IN (` + ph + `)
		  AND COALESCE(json_extract(a.metadata,'$.source'),'')<>'personal_learning_evidence'
		  AND COALESCE(json_extract(b.metadata,'$.source'),'')<>'personal_learning_evidence'
		  AND l.source_id <> l.target_id
		ORDER BY l.weight DESC, l.source_id, l.target_id
		LIMIT ?`
	args := make([]any, 0, 2*len(scopes)+1)
	for i := 0; i < 2; i++ {
		for _, sc := range scopes {
			args = append(args, sc)
		}
	}
	args = append(args, graphMaxEdgeRows)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var links []graphLink
	kinds := map[string]string{}
	for rows.Next() {
		var l graphLink
		var ma, mb string
		if err := rows.Scan(&l.a, &l.b, &l.relation, &l.weight, &ma, &mb); err != nil {
			return nil, nil, err
		}
		kinds[l.a], kinds[l.b] = ma, mb
		links = append(links, l)
	}
	return links, kinds, rows.Err()
}

func (s *SQLiteStore) countVisible(ctx context.Context, scopes []string) (map[string]int, error) {
	ph := strings.TrimSuffix(strings.Repeat("?,", len(scopes)), ",")
	args := make([]any, len(scopes))
	for i, sc := range scopes {
		args[i] = sc
	}
	rows, err := s.db.QueryContext(ctx, `SELECT modality, COUNT(*) FROM memories
		WHERE status='active' AND scope IN (`+ph+`)
		  AND COALESCE(json_extract(metadata,'$.source'),'')<>'personal_learning_evidence'
		GROUP BY modality`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var m string
		var n int
		if err := rows.Scan(&m, &n); err != nil {
			return nil, err
		}
		out[m] = n
	}
	return out, rows.Err()
}

func graphTitle(r Record) string {
	for _, k := range []string{"topic", "title"} {
		if v, ok := r.Metadata[k].(string); ok && strings.TrimSpace(v) != "" {
			return clipRunes(strings.TrimSpace(v), graphTitleRunes)
		}
	}
	line := strings.TrimSpace(r.Content)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = strings.TrimSpace(line[:i])
	}
	return clipRunes(line, graphTitleRunes)
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// Graph returns the memory graph described by req.
//
// Overview ranks the linked memories by how many links they have and returns the
// top ones. Ego walks outward from one memory, nearest and strongest first,
// until the limit; its Total counts what was reached before the limit stopped
// the walk, so Truncated is exact even when Total is a lower bound.
func (s *SQLiteStore) Graph(ctx context.Context, req GraphRequest) (GraphData, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = GraphModeOverview
	}
	if mode != GraphModeOverview && mode != GraphModeEgo {
		return GraphData{}, fmt.Errorf("unknown graph mode %q", req.Mode)
	}
	center := strings.TrimSpace(req.Center)
	if mode == GraphModeEgo && center == "" {
		return GraphData{}, errors.New("ego view needs a centre memory")
	}
	limit := clampInt(req.Limit, 1, graphMaxNodes, graphDefaultNodes)
	scopes := graphScopes(req.Scope)

	links, kinds, err := s.visibleLinks(ctx, scopes)
	if err != nil {
		return GraphData{}, err
	}
	kindSet := map[string]bool{}
	for _, m := range req.Modalities {
		if m = strings.ToLower(strings.TrimSpace(m)); m != "" {
			kindSet[m] = true
		}
	}
	allowed := func(id string) bool {
		if len(kindSet) == 0 || (mode == GraphModeEgo && id == center) {
			return true
		}
		return kindSet[strings.ToLower(kinds[id])]
	}

	adj := map[string][]graphLink{}
	degree := map[string]int{}
	var kept []graphLink
	for _, l := range links {
		if !allowed(l.a) || !allowed(l.b) {
			continue
		}
		kept = append(kept, l)
		adj[l.a] = append(adj[l.a], l)
		adj[l.b] = append(adj[l.b], l)
		degree[l.a]++
		degree[l.b]++
	}

	meta := GraphMeta{Mode: mode, Scope: scopes[len(scopes)-1], Groups: map[string]int{}}
	var chosen []string
	switch mode {
	case GraphModeOverview:
		ids := make([]string, 0, len(degree))
		for id := range degree {
			ids = append(ids, id)
		}
		meta.Total = len(ids)
		imp, err := s.importanceOf(ctx, ids)
		if err != nil {
			return GraphData{}, err
		}
		sort.Slice(ids, func(i, j int) bool {
			a, b := ids[i], ids[j]
			if degree[a] != degree[b] {
				return degree[a] > degree[b]
			}
			if imp[a] != imp[b] {
				return imp[a] > imp[b]
			}
			return a < b
		})
		if len(ids) > limit {
			ids = ids[:limit]
		}
		chosen = ids
		if counts, err := s.countVisible(ctx, scopes); err == nil {
			matching := 0
			for kind, n := range counts {
				if len(kindSet) == 0 || kindSet[strings.ToLower(kind)] {
					matching += n
				}
			}
			if matching > meta.Total {
				meta.Isolated = matching - meta.Total
			}
		}
	case GraphModeEgo:
		depth := clampInt(req.Depth, 1, graphMaxDepth, 1)
		meta.Center, meta.Depth = center, depth
		if _, linked := degree[center]; !linked && !s.visibleRecord(ctx, scopes, center) {
			return GraphData{}, ErrGraphNotFound
		}
		seen := map[string]bool{center: true}
		chosen = []string{center}
		frontier := []string{center}
		reached := 1
		for d := 0; d < depth && len(frontier) > 0; d++ {
			best := map[string]float64{}
			for _, id := range frontier {
				for _, l := range adj[id] {
					other := l.a
					if other == id {
						other = l.b
					}
					if seen[other] {
						continue
					}
					if w, ok := best[other]; !ok || l.weight > w {
						best[other] = l.weight
					}
				}
			}
			next := make([]string, 0, len(best))
			for id := range best {
				next = append(next, id)
			}
			sort.Slice(next, func(i, j int) bool {
				a, b := next[i], next[j]
				if best[a] != best[b] {
					return best[a] > best[b]
				}
				if degree[a] != degree[b] {
					return degree[a] > degree[b]
				}
				return a < b
			})
			reached += len(next)
			var again []string
			for _, id := range next {
				seen[id] = true // counted once, shown or not
				if len(chosen) < limit {
					chosen = append(chosen, id)
					again = append(again, id)
				}
			}
			frontier = again
		}
		meta.Total = reached
	}

	nodes, err := s.graphNodes(ctx, chosen, degree)
	if err != nil {
		return GraphData{}, err
	}
	have := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		have[n.ID] = true
		meta.Groups[n.Modality]++
	}
	edges := []GraphEdge{}
	for _, l := range kept {
		if have[l.a] && have[l.b] {
			edges = append(edges, GraphEdge{Source: l.a, Target: l.b, Relation: l.relation, Weight: l.weight})
		}
	}
	meta.Returned = len(nodes)
	meta.Truncated = meta.Returned < meta.Total
	return GraphData{Nodes: nodes, Edges: edges, Meta: meta}, nil
}
func (s *SQLiteStore) visibleRecord(ctx context.Context, scopes []string, id string) bool {
	r, err := s.GetRecord(ctx, id)
	if err != nil || r.Status != "active" {
		return false
	}
	if src, _ := r.Metadata["source"].(string); src == "personal_learning_evidence" {
		return false
	}
	for _, sc := range scopes {
		if r.Scope == sc {
			return true
		}
	}
	return false
}

func (s *SQLiteStore) importanceOf(ctx context.Context, ids []string) (map[string]float64, error) {
	out := make(map[string]float64, len(ids))
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(importance,0) FROM memories WHERE id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var v float64
			if err := rows.Scan(&id, &v); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = v
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// graphNodes loads the chosen records, keeping the order of ids.
func (s *SQLiteStore) graphNodes(ctx context.Context, ids []string, degree map[string]int) ([]GraphNode, error) {
	byID := make(map[string]GraphNode, len(ids))
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT `+selectCols+` FROM memories m WHERE m.id IN (`+
			strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			r, err := s.scanRecord(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			byID[r.ID] = GraphNode{ID: r.ID, Title: graphTitle(r), Modality: r.Modality, Scope: r.Scope,
				Importance: r.Importance, Canonical: r.IsCanonical, Degree: degree[r.ID]}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	out := make([]GraphNode, 0, len(ids))
	for _, id := range ids {
		if n, ok := byID[id]; ok {
			out = append(out, n)
		}
	}
	return out, nil
}
