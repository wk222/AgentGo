package apps

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// InnerApp is a system-integrated app (UI bundle and/or workflow/agent backend).
type InnerApp struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Kind         string            `json:"kind"` // workflow | agent | ui
	WorkflowID   string            `json:"workflow_id,omitempty"`
	SystemPrompt string            `json:"system_prompt,omitempty"`
	BundlePath   string            `json:"bundle_path,omitempty"`
	Exports      []string          `json:"exports,omitempty"`
	Enabled      bool              `json:"enabled"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CreatedAt    int64             `json:"created_at"`
	URL          string            `json:"url,omitempty"` // UI hint for desktop
}

type Port struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"` // string, json, file, message
	Required bool   `json:"required"`
}

// MatrixNode represents a node in the App Matrix orchestration graph.
type MatrixNode struct {
	ID          string            `json:"id"`
	AppID       string            `json:"app_id"` // which InnerApp to invoke
	Capability  string            `json:"capability,omitempty"`
	DomainLabel string            `json:"domain_label,omitempty"`
	InputPorts  []Port            `json:"input_ports,omitempty"`
	OutputPorts []Port            `json:"output_ports,omitempty"`
	Config      map[string]string `json:"config,omitempty"`
	Status      string            `json:"status,omitempty"` // healthy, degraded, failed
	LastRunAt   int64             `json:"last_run_at,omitempty"`
	LastError   string            `json:"last_error,omitempty"`
}

// MatrixEdge represents a connection between two nodes in the App Matrix.
type MatrixEdge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	When     string `json:"when,omitempty"` // conditional execution
	FromPort string `json:"from_port,omitempty"`
	ToPort   string `json:"to_port,omitempty"`
}

// MatrixOrchestration represents a saved pipeline of interacting InnerApps.
type MatrixOrchestration struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Nodes       []MatrixNode `json:"nodes"`
	Edges       []MatrixEdge `json:"edges"`
	Schedule    string       `json:"schedule,omitempty"`
	Validated   bool         `json:"validated"`
	ValidateErr string       `json:"validate_error,omitempty"`
	CreatedAt   int64        `json:"created_at"`
	UpdatedAt   int64        `json:"updated_at"`
}

// Store persists inner apps and matrix orchestrations in SQLite.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS inner_apps (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT,
  kind TEXT NOT NULL,
  workflow_id TEXT,
  system_prompt TEXT,
  bundle_path TEXT,
  exports TEXT,
  enabled INTEGER NOT NULL DEFAULT 1,
  metadata TEXT,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS app_orchestrations (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT,
  nodes_json TEXT NOT NULL,
  edges_json TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
`)
	if err != nil {
		return nil, err
	}
	_ = migrateInnerApps(s.db)
	_ = migrateOrchestrations(s.db)
	return s, nil
}

// ... existing code ...

func migrateInnerApps(db *sql.DB) error {
	cols := []string{
		`ALTER TABLE inner_apps ADD COLUMN bundle_path TEXT`,
		`ALTER TABLE inner_apps ADD COLUMN exports TEXT`,
		`ALTER TABLE inner_apps ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1`,
	}
	for _, q := range cols {
		_, _ = db.Exec(q)
	}
	return nil
}

func migrateOrchestrations(db *sql.DB) error {
	cols := []string{
		`ALTER TABLE app_orchestrations ADD COLUMN schedule TEXT`,
		`ALTER TABLE app_orchestrations ADD COLUMN validated INTEGER DEFAULT 0`,
		`ALTER TABLE app_orchestrations ADD COLUMN validate_error TEXT`,
	}
	for _, q := range cols {
		_, _ = db.Exec(q)
	}
	return nil
}

func (s *Store) Upsert(ctx context.Context, a InnerApp) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt == 0 {
		a.CreatedAt = time.Now().Unix()
	}
	if a.Kind == "" {
		a.Kind = "workflow"
	}
	enabled := 1
	if !a.Enabled {
		enabled = 0
	}
	meta, _ := json.Marshal(a.Metadata)
	exp, _ := json.Marshal(a.Exports)
	_, err := s.db.ExecContext(ctx, `
INSERT INTO inner_apps (id, name, description, kind, workflow_id, system_prompt, bundle_path, exports, enabled, metadata, created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET
  description=excluded.description, kind=excluded.kind, workflow_id=excluded.workflow_id,
  system_prompt=excluded.system_prompt, bundle_path=excluded.bundle_path,
  exports=excluded.exports, enabled=excluded.enabled, metadata=excluded.metadata`,
		a.ID, a.Name, a.Description, a.Kind, a.WorkflowID, a.SystemPrompt,
		a.BundlePath, string(exp), enabled, string(meta), a.CreatedAt)
	return err
}

func (s *Store) GetByName(ctx context.Context, name string) (InnerApp, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, description, kind, workflow_id, system_prompt, bundle_path, exports, enabled, metadata, created_at
FROM inner_apps WHERE name=?`, name)
	return scanApp(row)
}

func (s *Store) GetByID(ctx context.Context, id string) (InnerApp, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, description, kind, workflow_id, system_prompt, bundle_path, exports, enabled, metadata, created_at
FROM inner_apps WHERE id=?`, id)
	return scanApp(row)
}

func (s *Store) List(ctx context.Context, limit int) ([]InnerApp, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, description, kind, workflow_id, system_prompt, bundle_path, exports, enabled, metadata, created_at
FROM inner_apps ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InnerApp
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return out, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanApp(row interface{ Scan(...any) error }) (InnerApp, error) {
	var a InnerApp
	var meta, exp sql.NullString
	var wf, prompt, bundle sql.NullString
	var enabled int
	err := row.Scan(&a.ID, &a.Name, &a.Description, &a.Kind, &wf, &prompt, &bundle, &exp, &enabled, &meta, &a.CreatedAt)
	if wf.Valid {
		a.WorkflowID = wf.String
	}
	if prompt.Valid {
		a.SystemPrompt = prompt.String
	}
	if bundle.Valid {
		a.BundlePath = bundle.String
	}
	a.Enabled = enabled != 0
	if exp.Valid && exp.String != "" {
		_ = json.Unmarshal([]byte(exp.String), &a.Exports)
	}
	if meta.Valid && meta.String != "" {
		_ = json.Unmarshal([]byte(meta.String), &a.Metadata)
	}
	if a.Metadata == nil {
		a.Metadata = map[string]string{}
	}
	a.URL = "app://" + a.Name
	if a.Kind == "ui" {
		a.URL = "app://" + a.Name + "/"
	}
	return a, err
}

// ParseExports splits comma-separated export names.
func ParseExports(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Store) SaveMatrix(ctx context.Context, m MatrixOrchestration) error {
	if m.ID == "" {
		m.ID = "matrix_" + uuid.NewString()[:12]
	}
	now := time.Now().Unix()
	if m.CreatedAt == 0 {
		m.CreatedAt = now
	}
	m.UpdatedAt = now

	nodesJSON, _ := json.Marshal(m.Nodes)
	edgesJSON, _ := json.Marshal(m.Edges)

	validatedVal := 0
	if m.Validated {
		validatedVal = 1
	}

	_, err := s.db.ExecContext(ctx, `
INSERT INTO app_orchestrations (id, name, description, nodes_json, edges_json, schedule, validated, validate_error, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET
  description=excluded.description, nodes_json=excluded.nodes_json, edges_json=excluded.edges_json,
  schedule=excluded.schedule, validated=excluded.validated, validate_error=excluded.validate_error, updated_at=excluded.updated_at
`, m.ID, m.Name, m.Description, string(nodesJSON), string(edgesJSON), m.Schedule, validatedVal, m.ValidateErr, m.CreatedAt, m.UpdatedAt)
	return err
}

func (s *Store) GetMatrixByName(ctx context.Context, name string) (*MatrixOrchestration, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, description, nodes_json, edges_json, schedule, validated, validate_error, created_at, updated_at
FROM app_orchestrations WHERE name=?`, name)

	var m MatrixOrchestration
	var nodesStr, edgesStr string
	var scheduleVal, validateErrVal sql.NullString
	var validatedVal int
	err := row.Scan(&m.ID, &m.Name, &m.Description, &nodesStr, &edgesStr, &scheduleVal, &validatedVal, &validateErrVal, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(nodesStr), &m.Nodes)
	_ = json.Unmarshal([]byte(edgesStr), &m.Edges)
	m.Validated = validatedVal > 0
	if scheduleVal.Valid {
		m.Schedule = scheduleVal.String
	}
	if validateErrVal.Valid {
		m.ValidateErr = validateErrVal.String
	}
	return &m, nil
}
