package sessions

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// chat_session_experts binds a conversation to an expert (internal/experts).
// Only the id is stored: the rules are read from the expert's file on every run,
// so editing the file changes the next turn, and a file that is gone just means
// the conversation runs without an expert. No foreign key, like session_modes:
// ACP and gateway sessions may be bound before (or without) a chat_sessions row.
const sessionExpertsSchema = `
CREATE TABLE IF NOT EXISTS chat_session_experts (
	session_id TEXT PRIMARY KEY,
	expert_id  TEXT NOT NULL,
	bound_at   INTEGER NOT NULL
);`

// SetExpert binds a conversation to an expert, replacing an earlier binding. An
// empty expertID removes the binding.
func (s *Store) SetExpert(ctx context.Context, sessionID, expertID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("empty session id")
	}
	expertID = strings.TrimSpace(expertID)
	if expertID == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM chat_session_experts WHERE session_id = ?`, sessionID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_session_experts (session_id, expert_id, bound_at) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET expert_id = excluded.expert_id, bound_at = excluded.bound_at
	`, sessionID, expertID, time.Now().Unix())
	return err
}

// Expert returns the id of the expert a conversation is bound to, "" when it has none.
func (s *Store) Expert(ctx context.Context, sessionID string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT expert_id FROM chat_session_experts WHERE session_id = ?`, strings.TrimSpace(sessionID)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}
