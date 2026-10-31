package tools

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DynamicToolDef is a persisted tool definition (PyBot ToolStorage subset).
type DynamicToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  string `json:"parameters"`
	Code        string `json:"code"`
	UsageGuide  string `json:"usage_guide"`
	// EnvVars names the host environment variables this tool may read. Tool
	// processes get a filtered environment (see buildToolEnv): secret-looking
	// variables are stripped unless the tool explicitly declares them here.
	EnvVars   []string `json:"env_vars,omitempty"`
	CreatedAt int64    `json:"created_at"`
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
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS dynamic_tools (
  name TEXT PRIMARY KEY,
  description TEXT NOT NULL,
  parameters TEXT,
  code TEXT,
  usage_guide TEXT,
  created_at INTEGER NOT NULL
)`); err != nil {
		return err
	}
	// Older databases lack env_vars; adding it twice is a harmless error.
	if _, err := s.db.Exec(`ALTER TABLE dynamic_tools ADD COLUMN env_vars TEXT`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	return nil
}

func (s *DynamicStore) Save(def DynamicToolDef) error {
	if def.Name == "" {
		return fmt.Errorf("tool name required")
	}
	if def.CreatedAt == 0 {
		def.CreatedAt = time.Now().Unix()
	}
	envJSON, _ := json.Marshal(def.EnvVars)
	_, err := s.db.Exec(
		`INSERT INTO dynamic_tools (name, description, parameters, code, usage_guide, created_at, env_vars)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   description=excluded.description,
		   parameters=excluded.parameters,
		   code=excluded.code,
		   usage_guide=excluded.usage_guide,
		   env_vars=excluded.env_vars`,
		def.Name, def.Description, def.Parameters, def.Code, def.UsageGuide, def.CreatedAt, string(envJSON),
	)
	return err
}

func (s *DynamicStore) List() ([]DynamicToolDef, error) {
	rows, err := s.db.Query(`SELECT name, description, parameters, code, usage_guide, created_at, COALESCE(env_vars, '') FROM dynamic_tools ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DynamicToolDef
	for rows.Next() {
		var d DynamicToolDef
		var envJSON string
		if err := rows.Scan(&d.Name, &d.Description, &d.Parameters, &d.Code, &d.UsageGuide, &d.CreatedAt, &envJSON); err != nil {
			return nil, err
		}
		if envJSON != "" {
			_ = json.Unmarshal([]byte(envJSON), &d.EnvVars)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Delete removes a persisted definition. Used to roll back a create whose
// registration failed, so the store never keeps a tool that cannot run.
func (s *DynamicStore) Delete(name string) error {
	_, err := s.db.Exec(`DELETE FROM dynamic_tools WHERE name = ?`, name)
	return err
}

func (s *DynamicStore) Get(name string) (DynamicToolDef, error) {
	var d DynamicToolDef
	var envJSON string
	err := s.db.QueryRow(
		`SELECT name, description, parameters, code, usage_guide, created_at, COALESCE(env_vars, '') FROM dynamic_tools WHERE name = ?`, name,
	).Scan(&d.Name, &d.Description, &d.Parameters, &d.Code, &d.UsageGuide, &d.CreatedAt, &envJSON)
	if envJSON != "" {
		_ = json.Unmarshal([]byte(envJSON), &d.EnvVars)
	}
	return d, err
}

// MarshalSummary returns JSON for list_dynamic_tools.
func (d DynamicToolDef) MarshalSummary() string {
	b, _ := json.Marshal(map[string]any{
		"name": d.Name, "description": d.Description, "created_at": d.CreatedAt,
	})
	return string(b)
}
