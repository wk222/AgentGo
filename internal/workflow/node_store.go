package workflow

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) initNodeTables() error {
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS workflow_nodes (
  id TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  retired INTEGER NOT NULL DEFAULT 0,
  body TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS workflow_node_versions (
  node_id TEXT NOT NULL,
  version INTEGER NOT NULL,
  body TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (node_id, version)
)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

// SaveNode stores spec as the next version of its id (1 for a new node) and
// keeps every version for rollback. Saving revives a retired node.
func (s *Store) SaveNode(spec NodeSpec) (NodeManifest, error) {
	if err := spec.Normalize(); err != nil {
		return NodeManifest{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return NodeManifest{}, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()
	m := NodeManifest{NodeSpec: spec, Version: 1, CreatedAt: now, UpdatedAt: now}
	var oldVersion int
	var oldCreated int64
	switch err := tx.QueryRow(`SELECT version, created_at FROM workflow_nodes WHERE id = ?`, spec.ID).Scan(&oldVersion, &oldCreated); {
	case err == nil:
		m.Version, m.CreatedAt = oldVersion+1, oldCreated
	case errors.Is(err, sql.ErrNoRows):
	default:
		return NodeManifest{}, err
	}
	body, err := json.Marshal(m)
	if err != nil {
		return NodeManifest{}, err
	}
	if _, err := tx.Exec(`INSERT INTO workflow_nodes(id, version, retired, body, created_at, updated_at)
VALUES(?, ?, 0, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET version = excluded.version, retired = 0, body = excluded.body, updated_at = excluded.updated_at`,
		m.ID, m.Version, string(body), m.CreatedAt, m.UpdatedAt); err != nil {
		return NodeManifest{}, err
	}
	if _, err := tx.Exec(`INSERT INTO workflow_node_versions(node_id, version, body, created_at) VALUES(?, ?, ?, ?)`,
		m.ID, m.Version, string(body), now); err != nil {
		return NodeManifest{}, err
	}
	if err := tx.Commit(); err != nil {
		return NodeManifest{}, err
	}
	return m, nil
}

func decodeNode(body string, version int, retired bool) (NodeManifest, error) {
	var m NodeManifest
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return NodeManifest{}, err
	}
	m.Version, m.Retired = version, retired
	return m, nil
}

// GetNode returns the current version of a node (retired ones included).
func (s *Store) GetNode(id string) (NodeManifest, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	var body string
	var version, retired int
	if err := s.db.QueryRow(`SELECT body, version, retired FROM workflow_nodes WHERE id = ?`, id).Scan(&body, &version, &retired); err != nil {
		return NodeManifest{}, false
	}
	m, err := decodeNode(body, version, retired != 0)
	if err != nil {
		return NodeManifest{}, false
	}
	return m, true
}

// GetNodeVersion returns one historical version of a node.
func (s *Store) GetNodeVersion(id string, version int) (NodeManifest, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	var body string
	if err := s.db.QueryRow(`SELECT body FROM workflow_node_versions WHERE node_id = ? AND version = ?`, id, version).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NodeManifest{}, fmt.Errorf("node %q has no version %d", id, version)
		}
		return NodeManifest{}, err
	}
	return decodeNode(body, version, false)
}

// NodeVersions lists the stored versions of a node, oldest first.
func (s *Store) NodeVersions(id string) ([]int, error) {
	rows, err := s.db.Query(`SELECT version FROM workflow_node_versions WHERE node_id = ? ORDER BY version`, strings.ToLower(strings.TrimSpace(id)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListNodes returns the current version of every node, ordered by id.
func (s *Store) ListNodes(includeRetired bool) ([]NodeManifest, error) {
	rows, err := s.db.Query(`SELECT body, version, retired FROM workflow_nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NodeManifest
	for rows.Next() {
		var body string
		var version, retired int
		if err := rows.Scan(&body, &version, &retired); err != nil {
			return nil, err
		}
		if retired != 0 && !includeRetired {
			continue
		}
		m, err := decodeNode(body, version, retired != 0)
		if err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RetireNode stops workflows from running a node without deleting it or its history.
func (s *Store) RetireNode(id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	res, err := s.db.Exec(`UPDATE workflow_nodes SET retired = 1, updated_at = ? WHERE id = ?`, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("node %q not found", id)
	}
	return nil
}

// NodeUsage lists the workflows that contain a node of the given custom node id.
func (s *Store) NodeUsage(id string) []string {
	id = strings.ToLower(strings.TrimSpace(id))
	defs, err := s.List()
	if err != nil {
		return nil
	}
	var used []string
	for _, d := range defs {
		for _, n := range d.Nodes {
			if CustomNodeID(n) == id {
				used = append(used, d.ID)
				break
			}
		}
	}
	return used
}
