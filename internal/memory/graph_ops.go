package memory

import (
	"context"
	"time"
)

// MemoryLink is an edge in Graph-Lite.
type MemoryLink struct {
	SourceID string  `json:"source_id"`
	TargetID string  `json:"target_id"`
	Relation string  `json:"relation"`
	Weight   float64 `json:"weight"`
}

// GraphView is a small subgraph for the memory console.
type GraphView struct {
	Nodes []Record      `json:"nodes"`
	Edges []MemoryLink  `json:"edges"`
}

// UpdateRecord patches scope/modality/content/importance (memory console).
func (s *SQLiteStore) UpdateRecord(ctx context.Context, id string, patch Record) error {
	r, err := s.GetRecord(ctx, id)
	if err != nil {
		return err
	}
	if patch.Content != "" {
		r.Content = patch.Content
	}
	if patch.Scope != "" {
		r.Scope = patch.Scope
	}
	if patch.Modality != "" {
		r.Modality = patch.Modality
	}
	if patch.Importance > 0 {
		r.Importance = patch.Importance
	}
	if patch.Metadata != nil {
		r.Metadata = patch.Metadata
	}
	r.UpdatedAt = time.Now().Unix()
	return s.Ingest(ctx, r)
}

func (s *SQLiteStore) GetRecord(ctx context.Context, id string) (Record, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+selectCols+` FROM memories m WHERE id = ?`, id)
	return s.scanRecord(row)
}

func (s *SQLiteStore) ListLinks(ctx context.Context, memoryID string, limit int) ([]MemoryLink, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT source_id, target_id, relation, weight FROM memory_links
		WHERE source_id = ? OR target_id = ?
		ORDER BY weight DESC LIMIT ?`, memoryID, memoryID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryLink
	for rows.Next() {
		var l MemoryLink
		if err := rows.Scan(&l.SourceID, &l.TargetID, &l.Relation, &l.Weight); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

// BuildGraphView returns center node + 1-hop neighbors (recall graph-lite for UI).
func (s *SQLiteStore) BuildGraphView(ctx context.Context, centerID string, limit int) (GraphView, error) {
	if limit <= 0 {
		limit = 24
	}
	var gv GraphView
	center, err := s.GetRecord(ctx, centerID)
	if err != nil {
		return gv, err
	}
	gv.Nodes = append(gv.Nodes, center)
	links, _ := s.ListLinks(ctx, centerID, limit)
	gv.Edges = links
	seen := map[string]bool{centerID: true}
	for _, l := range links {
		for _, nid := range []string{l.SourceID, l.TargetID} {
			if seen[nid] || nid == centerID {
				continue
			}
			r, err := s.GetRecord(ctx, nid)
			if err == nil && r.Status == "active" {
				gv.Nodes = append(gv.Nodes, r)
				seen[nid] = true
			}
		}
	}
	return gv, nil
}
