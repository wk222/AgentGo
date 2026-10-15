// Package db provides shared SQLite connection settings for AgentGo desktop runtime.
package db

import (
	"database/sql"
	"fmt"
)

// Configure applies pragmas recommended for a single shared *sql.DB on agentgo.db.
// Keeping MaxOpenConns(1) guarantees that connection-scoped pragmas (foreign_keys, busy_timeout),
// in-memory ":memory:" test stores, and read-then-write sequence allocation remain atomic.
func Configure(conn *sql.DB) error {
	if conn == nil {
		return fmt.Errorf("db is nil")
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=10000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := conn.Exec(pragma); err != nil {
			return fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return nil
}
