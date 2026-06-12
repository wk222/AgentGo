package capability

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Store persists CapabilityBus grants and metrics.
type Store struct {
	db *sql.DB
}

// MetricRecord stores execution time and cost metrics.
type MetricRecord struct {
	Component string  `json:"component"`
	Name      string  `json:"name"`
	Duration  float64 `json:"duration_ms"`
	Tokens    int     `json:"tokens"`
	Cost      float64 `json:"cost"`
	CreatedAt int64   `json:"created_at"`
}

// AssetStats aggregates telemetry metrics for a capability asset.
type AssetStats struct {
	CallCount  int     `json:"call_count"`
	FailCount  int     `json:"fail_count"`
	AvgLatency float64 `json:"avg_latency_ms"`
	LastUsed   int64   `json:"last_used"`
}

func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS capability_grants (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  name TEXT NOT NULL,
  scope TEXT NOT NULL,
  metadata TEXT,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS capability_metrics (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  component TEXT NOT NULL,
  name TEXT NOT NULL,
  duration_ms REAL,
  tokens INTEGER,
  cost REAL,
  created_at INTEGER NOT NULL
);
`)
	if err != nil {
		return nil, err
	}

	// Defensively execute schema migrations for older local databases
	newCols := []struct {
		name string
		typ  string
	}{
		{"version", "TEXT"},
		{"status", "TEXT"},
		{"owner", "TEXT"},
		{"source", "TEXT"},
		{"risk_level", "TEXT"},
		{"reusable", "INTEGER DEFAULT 1"},
		{"recommended", "INTEGER DEFAULT 0"},
		{"supersedes_id", "TEXT"},
		{"last_verified_at", "INTEGER DEFAULT 0"},
		{"verify_result", "TEXT"},
		{"updated_at", "INTEGER DEFAULT 0"},
	}
	for _, col := range newCols {
		// Ignore column exists error (e.g. duplicate column name)
		_, _ = s.db.Exec(fmt.Sprintf("ALTER TABLE capability_grants ADD COLUMN %s %s", col.name, col.typ))
	}

	return s, nil
}

func (s *Store) Upsert(g Grant) error {
	meta, _ := json.Marshal(g.Metadata)
	if g.CreatedAt == 0 {
		g.CreatedAt = time.Now().Unix()
	}
	if g.UpdatedAt == 0 {
		g.UpdatedAt = time.Now().Unix()
	}
	reusableVal := 0
	if g.Reusable {
		reusableVal = 1
	}
	recommendedVal := 0
	if g.Recommended {
		recommendedVal = 1
	}
	_, err := s.db.Exec(`INSERT INTO capability_grants (
		id, kind, name, scope, version, status, owner, source, risk_level, reusable, recommended, supersedes_id, metadata, last_verified_at, verify_result, created_at, updated_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(id) DO UPDATE SET 
		version=excluded.version, status=excluded.status, owner=excluded.owner, source=excluded.source, risk_level=excluded.risk_level, 
		reusable=excluded.reusable, recommended=excluded.recommended, supersedes_id=excluded.supersedes_id, metadata=excluded.metadata, 
		last_verified_at=excluded.last_verified_at, verify_result=excluded.verify_result, updated_at=excluded.updated_at`,
		g.ID, g.Kind, g.Name, g.Scope, g.Version, string(g.Status), g.Owner, g.Source, g.RiskLevel, reusableVal, recommendedVal, g.SupersedesID, string(meta), g.LastVerifiedAt, g.VerifyResult, g.CreatedAt, g.UpdatedAt)
	return err
}

func (s *Store) RecordMetric(m MetricRecord) error {
	if m.CreatedAt == 0 {
		m.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(`INSERT INTO capability_metrics (component, name, duration_ms, tokens, cost, created_at) VALUES (?,?,?,?,?,?)`,
		m.Component, m.Name, m.Duration, m.Tokens, m.Cost, m.CreatedAt)
	return err
}

func (s *Store) List(kind string) ([]Grant, error) {
	query := `SELECT 
		id, kind, name, scope, version, status, owner, source, risk_level, reusable, recommended, supersedes_id, metadata, last_verified_at, verify_result, created_at, updated_at 
		FROM capability_grants`
	var rows *sql.Rows
	var err error
	if kind != "" {
		rows, err = s.db.Query(query+` WHERE kind = ? ORDER BY created_at DESC`, kind)
	} else {
		rows, err = s.db.Query(query + ` ORDER BY created_at DESC`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Grant
	for rows.Next() {
		var g Grant
		var meta string
		var statusStr string
		var reusableInt, recommendedInt int
		err := rows.Scan(
			&g.ID, &g.Kind, &g.Name, &g.Scope, &g.Version, &statusStr, &g.Owner, &g.Source, &g.RiskLevel,
			&reusableInt, &recommendedInt, &g.SupersedesID, &meta, &g.LastVerifiedAt, &g.VerifyResult, &g.CreatedAt, &g.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		g.Status = AssetStatus(statusStr)
		g.Reusable = reusableInt > 0
		g.Recommended = recommendedInt > 0
		if meta != "" {
			_ = json.Unmarshal([]byte(meta), &g.Metadata)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) LoadAll() ([]Grant, error) {
	return s.List("")
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM capability_grants WHERE id = ?`, id)
	return err
}

func (s *Store) QueryStats(component, name string) (AssetStats, error) {
	var stats AssetStats
	row := s.db.QueryRow(`
		SELECT 
			COUNT(*), 
			SUM(CASE WHEN cost < 0 THEN 1 ELSE 0 END), 
			COALESCE(AVG(duration_ms), 0.0), 
			COALESCE(MAX(created_at), 0) 
		FROM capability_metrics 
		WHERE component = ? AND name = ?`, component, name)
	err := row.Scan(&stats.CallCount, &stats.FailCount, &stats.AvgLatency, &stats.LastUsed)
	return stats, err
}
