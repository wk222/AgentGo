// Package db provides shared SQLite connection settings for AgentGo desktop runtime.
package db

import (
	"database/sql"
	"fmt"
)

// SchemaVersion is the generation of the agentgo.db layout this build reads and writes. It is kept in
// SQLite's PRAGMA user_version. Raise it when a change would make an older build misbehave on the same
// file (a column it must not ignore, a table whose meaning changed); adding tables or columns does not
// need a new generation, because every build creates what it is missing.
const SchemaVersion = 1

// SchemaTooNewError means agentgo.db was last written by a newer build than this one.
type SchemaTooNewError struct{ Found, Supported int }

func (e *SchemaTooNewError) Error() string {
	return fmt.Sprintf("数据库(agentgo.db)由更新版本的 AgentGo 写入（数据库代数 %d，本程序最高支持 %d）。"+
		"为避免旧版本损坏数据，本程序拒绝打开它：请改用新版程序，数据不会受到影响。", e.Found, e.Supported)
}

// CheckSchemaVersion refuses a database written by a newer build. It changes nothing, so a refused
// database stays exactly as the newer build left it.
func CheckSchemaVersion(conn *sql.DB) error {
	var found int
	if err := conn.QueryRow("PRAGMA user_version").Scan(&found); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if found > SchemaVersion {
		return &SchemaTooNewError{Found: found, Supported: SchemaVersion}
	}
	return nil
}

// StampSchemaVersion records this build's generation once the schema is in place.
func StampSchemaVersion(conn *sql.DB) error {
	var found int
	if err := conn.QueryRow("PRAGMA user_version").Scan(&found); err == nil && found == SchemaVersion {
		return nil // already current: no write on every start
	}
	if _, err := conn.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
		return fmt.Errorf("stamp schema version: %w", err)
	}
	return nil
}

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
