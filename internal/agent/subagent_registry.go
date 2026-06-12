package agent

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"

	"agentgo/internal/db"
)

// SubagentDef is a persisted specialist agent profile (PyBot SubagentRegistry subset).
type SubagentDef struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Role         string `json:"role"`
	SystemPrompt string `json:"system_prompt"`
	ModelHint    string `json:"model_hint,omitempty"`
	Enabled      bool   `json:"enabled"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

// SubagentRegistry stores and resolves registered subagents.
type SubagentRegistry struct {
	db *sql.DB
}

func NewSubagentRegistry(conn *sql.DB) (*SubagentRegistry, error) {
	if err := db.Configure(conn); err != nil {
		return nil, err
	}
	r := &SubagentRegistry{db: conn}
	if err := r.migrate(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *SubagentRegistry) migrate() error {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS subagents (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			role TEXT,
			system_prompt TEXT NOT NULL,
			model_hint TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_subagents_enabled ON subagents(enabled);
	`)
	return err
}

func (r *SubagentRegistry) List(ctx context.Context, limit int) ([]SubagentDef, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, role, system_prompt, model_hint, enabled, created_at, updated_at
		FROM subagents ORDER BY name ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubagentDef
	for rows.Next() {
		var d SubagentDef
		var en int
		if err := rows.Scan(&d.ID, &d.Name, &d.Role, &d.SystemPrompt, &d.ModelHint, &en, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Enabled = en != 0
		out = append(out, d)
	}
	return out, nil
}

func (r *SubagentRegistry) Get(ctx context.Context, idOrName string) (SubagentDef, bool, error) {
	key := strings.TrimSpace(idOrName)
	if key == "" {
		return SubagentDef{}, false, nil
	}
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, role, system_prompt, model_hint, enabled, created_at, updated_at
		FROM subagents WHERE id = ? OR name = ? LIMIT 1`, key, key)
	var d SubagentDef
	var en int
	err := row.Scan(&d.ID, &d.Name, &d.Role, &d.SystemPrompt, &d.ModelHint, &en, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return SubagentDef{}, false, nil
	}
	if err != nil {
		return SubagentDef{}, false, err
	}
	d.Enabled = en != 0
	return d, true, nil
}

func (r *SubagentRegistry) Upsert(ctx context.Context, d SubagentDef) (SubagentDef, error) {
	now := time.Now().Unix()
	d.Name = strings.TrimSpace(d.Name)
	d.SystemPrompt = strings.TrimSpace(d.SystemPrompt)
	if d.Name == "" || d.SystemPrompt == "" {
		return SubagentDef{}, errSubagentInvalid
	}
	if d.ID == "" {
		d.ID = "sub_" + uuid.New().String()[:8]
	}
	if d.CreatedAt == 0 {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	en := 0
	if d.Enabled {
		en = 1
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO subagents (id, name, role, system_prompt, model_hint, enabled, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, role=excluded.role, system_prompt=excluded.system_prompt,
			model_hint=excluded.model_hint, enabled=excluded.enabled, updated_at=excluded.updated_at`,
		d.ID, d.Name, d.Role, d.SystemPrompt, d.ModelHint, en, d.CreatedAt, d.UpdatedAt)
	return d, err
}

func (r *SubagentRegistry) Delete(ctx context.Context, idOrName string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM subagents WHERE id = ? OR name = ?`, idOrName, idOrName)
	return err
}

func (r *SubagentRegistry) ResolveIDs(ctx context.Context, ids []string) ([]SubagentDef, error) {
	if len(ids) == 0 {
		return r.ListEnabled(ctx, 8)
	}
	var out []SubagentDef
	for _, id := range ids {
		d, ok, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if ok && d.Enabled {
			out = append(out, d)
		}
	}
	return out, nil
}

func (r *SubagentRegistry) ListEnabled(ctx context.Context, limit int) ([]SubagentDef, error) {
	if limit <= 0 {
		limit = 8
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, role, system_prompt, model_hint, enabled, created_at, updated_at
		FROM subagents WHERE enabled = 1 ORDER BY name ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubagentDef
	for rows.Next() {
		var d SubagentDef
		var en int
		if err := rows.Scan(&d.ID, &d.Name, &d.Role, &d.SystemPrompt, &d.ModelHint, &en, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		d.Enabled = true
		out = append(out, d)
	}
	return out, nil
}

// SeedDefaults inserts society-of-mind style presets when table is empty.
func (r *SubagentRegistry) SeedDefaults(ctx context.Context) (int, error) {
	var n int
	_ = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM subagents`).Scan(&n)
	if n > 0 {
		return 0, nil
	}
	presets := []SubagentDef{
		{Name: "researcher", Role: "研究员", SystemPrompt: "你是研究员子智能体：收集事实、列要点，避免臆测。", Enabled: true},
		{Name: "critic", Role: "质疑者", SystemPrompt: "你是质疑者子智能体：找漏洞、风险与反例，简洁有力。", Enabled: true},
		{Name: "synthesizer", Role: "综合者", SystemPrompt: "你是综合者子智能体：合并多方观点为可执行结论。", Enabled: true},
		{Name: "app_builder", Role: "应用构建", SystemPrompt: "你是应用构建专家。优先建议主 Agent 调用 build_inner_app_iteratively；需要细改文件时用 update_inner_app_file + verify_inner_app。", Enabled: true},
	}
	for _, p := range presets {
		if _, err := r.Upsert(ctx, p); err != nil {
			return 0, err
		}
	}
	return len(presets), nil
}

var errSubagentInvalid = errString("subagent: name and system_prompt required")

type errString string

func (e errString) Error() string { return string(e) }
