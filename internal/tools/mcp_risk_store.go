package tools

import (
	"context"
	"database/sql"
	"fmt"
)

// Uses the existing runtime pool; this object neither opens nor closes a DB.
type mcpRiskStore struct{ db *sql.DB }

func (r *Registry) SetMCPStateDB(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("mcp: nil state database")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS mcp_tool_risk (
 service TEXT NOT NULL, tool TEXT NOT NULL, risk INTEGER NOT NULL,
 PRIMARY KEY(service,tool));`); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mcpStarted || r.mcpClosed {
		return fmt.Errorf("mcp: configure state before loading servers")
	}
	r.mcpState = &mcpRiskStore{db: db}
	return nil
}

// 0=declared read-only, 1=unknown/write. Observations only raise the floor.
func (s *mcpRiskStore) observe(ctx context.Context, server string, risks map[string]int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for name, risk := range risks {
		if _, err = tx.ExecContext(ctx, `INSERT INTO mcp_tool_risk(service,tool,risk) VALUES (?,?,?) ON CONFLICT(service,tool) DO UPDATE SET risk=MAX(risk,excluded.risk)`, server, name, risk); err != nil {
			return err
		}
		if err = tx.QueryRowContext(ctx, `SELECT risk FROM mcp_tool_risk WHERE service=? AND tool=?`, server, name).Scan(&risk); err != nil {
			return err
		}
		risks[name] = risk
	}
	return tx.Commit()
}
