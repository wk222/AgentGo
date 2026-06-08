package checkpoint

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	_ "modernc.org/sqlite"
)

// SQLiteStore implements adk.CheckPointStore for session-scoped resume.
type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_checkpoints (
			id TEXT PRIMARY KEY,
			payload BLOB NOT NULL,
			updated_at INTEGER NOT NULL
		);
	`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS agent_checkpoint_history (
			id TEXT NOT NULL,
			version INTEGER NOT NULL,
			payload BLOB NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (id, version)
		);
	`); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Get(ctx context.Context, checkPointID string) ([]byte, bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT payload FROM agent_checkpoints WHERE id = ?`, checkPointID)
	var b []byte
	if err := row.Scan(&b); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return b, true, nil
}

func (s *SQLiteStore) Set(ctx context.Context, checkPointID string, checkPoint []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_checkpoints (id, payload, updated_at)
		VALUES (?, ?, unixepoch())
		ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, updated_at = unixepoch()
	`, checkPointID, checkPoint)
	if err != nil {
		return err
	}

	var maxVersion int
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) FROM agent_checkpoint_history WHERE id = ?
	`, checkPointID).Scan(&maxVersion)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_checkpoint_history (id, version, payload, created_at)
		VALUES (?, ?, ?, unixepoch())
	`, checkPointID, maxVersion+1, checkPoint)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CheckpointHistoryEntry logs history timestamps.
type CheckpointHistoryEntry struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	CreatedAt int64  `json:"created_at"`
}

// ListHistory returns version list for a given checkpoint.
func (s *SQLiteStore) ListHistory(ctx context.Context, checkPointID string) ([]CheckpointHistoryEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, version, created_at FROM agent_checkpoint_history 
		WHERE id = ? ORDER BY version DESC
	`, checkPointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []CheckpointHistoryEntry
	for rows.Next() {
		var entry CheckpointHistoryEntry
		if err := rows.Scan(&entry.ID, &entry.Version, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// GetVersionPayload pulls historical checkpoint state by specific version.
func (s *SQLiteStore) GetVersionPayload(ctx context.Context, checkPointID string, version int) ([]byte, bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT payload FROM agent_checkpoint_history WHERE id = ? AND version = ?
	`, checkPointID, version)
	var b []byte
	if err := row.Scan(&b); err != nil {
		if err == sql.ErrNoRows {
			return nil, false, nil
		}
		return nil, false, err
	}
	return b, true, nil
}

var (
	_ adk.CheckPointStore      = (*SQLiteStore)(nil)
	_ compose.CheckPointStore = (*SQLiteStore)(nil)
)

func CheckpointIDForSession(sessionID string) string {
	if sessionID == "" {
		sessionID = "desktop"
	}
	return fmt.Sprintf("sess_%s", sessionID)
}
