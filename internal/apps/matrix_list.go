package apps

import (
	"context"
	"encoding/json"
)

// MatrixSummary is a lightweight list entry for orchestration graphs.
type MatrixSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	NodeCount   int    `json:"node_count"`
	EdgeCount   int    `json:"edge_count"`
	UpdatedAt   int64  `json:"updated_at"`
}

// ListMatrices returns all saved orchestration graphs.
func (s *Store) ListMatrices(ctx context.Context) ([]MatrixSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, description, nodes_json, edges_json, updated_at
FROM app_orchestrations ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MatrixSummary
	for rows.Next() {
		var id, name, desc, nodesStr, edgesStr string
		var updated int64
		if err := rows.Scan(&id, &name, &desc, &nodesStr, &edgesStr, &updated); err != nil {
			return nil, err
		}
		var nodes []MatrixNode
		var edges []MatrixEdge
		_ = json.Unmarshal([]byte(nodesStr), &nodes)
		_ = json.Unmarshal([]byte(edgesStr), &edges)
		out = append(out, MatrixSummary{
			ID: id, Name: name, Description: desc,
			NodeCount: len(nodes), EdgeCount: len(edges), UpdatedAt: updated,
		})
	}
	return out, nil
}
