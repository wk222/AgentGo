package sessions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const contextEventsSchema = `
CREATE TABLE IF NOT EXISTS chat_context_events (
 session_id TEXT NOT NULL, scope TEXT NOT NULL, seq INTEGER NOT NULL,
 event_id TEXT NOT NULL, kind TEXT NOT NULL, payload BLOB NOT NULL,
 PRIMARY KEY(session_id,scope,seq), UNIQUE(session_id,scope,event_id)
);`

var ErrContextEventExists = errors.New("context event already exists")
var ErrContextCursor = errors.New("context event cursor not found")

type ContextEventRecord struct {
	ID, Kind string
	Payload  []byte
}

// AppendContextEvents commits a complete event batch or nothing. Bootstrap is
// conditional on an empty log, so legacy history can be imported only once.
func (s *Store) AppendContextEvents(ctx context.Context, sid, scope string, records []ContextEventRecord, bootstrap bool) error {
	if sid == "" || scope == "" {
		return errors.New("empty context session or scope")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var seq int64
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq),0) FROM chat_context_events WHERE session_id=? AND scope=?", sid, scope).Scan(&seq); err != nil {
		return err
	}
	if bootstrap && seq > 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, r := range records {
		if r.ID == "" || r.Kind == "" {
			return errors.New("empty context event identity")
		}
		if seen[r.ID] {
			return ErrContextEventExists
		}
		seen[r.ID] = true
		seq++
		_, err = tx.ExecContext(ctx, "INSERT INTO chat_context_events(session_id,scope,seq,event_id,kind,payload) VALUES(?,?,?,?,?,?)", sid, scope, seq, r.ID, r.Kind, r.Payload)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return fmt.Errorf("%w: %s", ErrContextEventExists, r.ID)
			}
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LoadContextEvents(ctx context.Context, sid, scope, after string, reverse bool, kinds []string, limit int) ([]ContextEventRecord, bool, error) {
	cursor := int64(0)
	if after != "" {
		if err := s.db.QueryRowContext(ctx, "SELECT seq FROM chat_context_events WHERE session_id=? AND scope=? AND event_id=?", sid, scope, after).Scan(&cursor); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, false, ErrContextCursor
			}
			return nil, false, err
		}
	}
	query := "SELECT event_id,kind,payload FROM chat_context_events WHERE session_id=? AND scope=?"
	args := []any{sid, scope}
	if after != "" {
		if reverse {
			query += " AND seq<?"
		} else {
			query += " AND seq>?"
		}
		args = append(args, cursor)
	}
	if len(kinds) > 0 {
		query += " AND kind IN (" + strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",") + ")"
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	if reverse {
		query += " ORDER BY seq DESC"
	} else {
		query += " ORDER BY seq ASC"
	}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit+1)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []ContextEventRecord
	for rows.Next() {
		var r ContextEventRecord
		if err := rows.Scan(&r.ID, &r.Kind, &r.Payload); err != nil {
			return nil, false, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := limit > 0 && len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, nil
}

// HasContextEvents probes the indexed event log without reading payloads.
func (s *Store) HasContextEvents(ctx context.Context, sid, scope string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM chat_context_events WHERE session_id=? AND scope=?)", sid, scope).Scan(&exists)
	return exists, err
}

// GetContextEvent reads one immutable record in the caller's session/scope.
func (s *Store) GetContextEvent(ctx context.Context, sid, scope, id string) (ContextEventRecord, error) {
	var r ContextEventRecord
	err := s.db.QueryRowContext(ctx, "SELECT event_id,kind,payload FROM chat_context_events WHERE session_id=? AND scope=? AND event_id=?", sid, scope, id).Scan(&r.ID, &r.Kind, &r.Payload)
	return r, err
}
