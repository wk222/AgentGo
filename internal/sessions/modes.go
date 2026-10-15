package sessions

import (
	"context"
	"strings"
	"time"
)

// DefaultModeKey stores the runner-wide default mode alongside per-session rows.
const DefaultModeKey = "__default__"

// No foreign key: ACP and gateway sessions may set a mode before (or without)
// a chat_sessions row existing.
const sessionModesSchema = `
CREATE TABLE IF NOT EXISTS session_modes (
	session_id TEXT PRIMARY KEY,
	profile TEXT NOT NULL DEFAULT '',
	canvas TEXT NOT NULL DEFAULT '',
	updated_at INTEGER
);`

// ModeRecord is the persisted profile/canvas pair; normalization is the agent's job.
type ModeRecord struct {
	Profile string
	Canvas  string
}

func (s *Store) SaveMode(ctx context.Context, sessionID string, m ModeRecord) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = DefaultModeKey
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_modes (session_id, profile, canvas, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET profile = excluded.profile, canvas = excluded.canvas, updated_at = excluded.updated_at
	`, sessionID, m.Profile, m.Canvas, time.Now().Unix())
	return err
}

func (s *Store) DeleteMode(ctx context.Context, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM session_modes WHERE session_id = ?`, sessionID)
	return err
}

// LoadModes returns the default mode (ok=false when never saved) and all per-session modes.
func (s *Store) LoadModes(ctx context.Context) (def ModeRecord, ok bool, perSession map[string]ModeRecord, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id, profile, canvas FROM session_modes`)
	if err != nil {
		return ModeRecord{}, false, nil, err
	}
	defer rows.Close()
	perSession = make(map[string]ModeRecord)
	for rows.Next() {
		var id string
		var m ModeRecord
		if err := rows.Scan(&id, &m.Profile, &m.Canvas); err != nil {
			return ModeRecord{}, false, nil, err
		}
		if id == DefaultModeKey {
			def, ok = m, true
			continue
		}
		perSession[id] = m
	}
	return def, ok, perSession, rows.Err()
}
