package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// DynamicToolDef is a persisted tool definition (PyBot ToolStorage subset).
type DynamicToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  string `json:"parameters"`
	Code        string `json:"code"`
	UsageGuide  string `json:"usage_guide"`
	CreatedAt   int64  `json:"created_at"`
}

// DynamicStore persists agent-created tool metadata in SQLite.
type DynamicStore struct {
	db *sql.DB
}

func NewDynamicStore(db *sql.DB) (*DynamicStore, error) {
	s := &DynamicStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *DynamicStore) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS dynamic_tools (
  name TEXT PRIMARY KEY,
  description TEXT NOT NULL,
  parameters TEXT,
  code TEXT,
  usage_guide TEXT,
  created_at INTEGER NOT NULL
)`)
	return err
}

func (s *DynamicStore) Save(def DynamicToolDef) error {
	if def.Name == "" {
		return fmt.Errorf("tool name required")
	}
	if def.CreatedAt == 0 {
		def.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO dynamic_tools (name, description, parameters, code, usage_guide, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   description=excluded.description,
		   parameters=excluded.parameters,
		   code=excluded.code,
		   usage_guide=excluded.usage_guide`,
		def.Name, def.Description, def.Parameters, def.Code, def.UsageGuide, def.CreatedAt,
	)
	return err
}

func (s *DynamicStore) List() ([]DynamicToolDef, error) {
	rows, err := s.db.Query(`SELECT name, description, parameters, code, usage_guide, created_at FROM dynamic_tools ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DynamicToolDef
	for rows.Next() {
		var d DynamicToolDef
		if err := rows.Scan(&d.Name, &d.Description, &d.Parameters, &d.Code, &d.UsageGuide, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *DynamicStore) Get(name string) (DynamicToolDef, error) {
	var d DynamicToolDef
	err := s.db.QueryRow(
		`SELECT name, description, parameters, code, usage_guide, created_at FROM dynamic_tools WHERE name = ?`, name,
	).Scan(&d.Name, &d.Description, &d.Parameters, &d.Code, &d.UsageGuide, &d.CreatedAt)
	return d, err
}

// MarshalSummary returns JSON for list_dynamic_tools.
func (d DynamicToolDef) MarshalSummary() string {
	b, _ := json.Marshal(map[string]any{
		"name": d.Name, "description": d.Description, "created_at": d.CreatedAt,
	})
	return string(b)
}
