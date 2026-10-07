package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrLaneNotFound  = errors.New("sessions: lane not found")
	ErrEntryNotFound = errors.New("sessions: entry not found")
)

// EntryKind classifies an immutable conversation tree node.
type EntryKind string

const (
	EntryKindMessage       EntryKind = "message"
	EntryKindCompaction    EntryKind = "compaction"
	EntryKindToolSnapshot  EntryKind = "tool_snapshot"
	EntryKindBranchSummary EntryKind = "branch_summary"
	EntryKindCustom        EntryKind = "custom"
)

// Entry represents an immutable node in the conversation history DAG.
type Entry struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id,omitempty"`
	SessionID string    `json:"session_id"`
	Kind      EntryKind `json:"kind"`
	Role      string    `json:"role,omitempty"`
	Content   string    `json:"content,omitempty"`
	MsgType   string    `json:"msg_type,omitempty"`
	MetaJSON  string    `json:"meta_json,omitempty"`
	CreatedAt int64     `json:"created_at"`
}

// Lane represents a named cursor or branch pointing to a leaf node in the Entry tree.
type Lane struct {
	Name        string `json:"name"`
	SessionID   string `json:"session_id"`
	HeadEntryID string `json:"head_entry_id,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// InitTreeSchema creates the database tables for Entry tree and Named Lanes.
func (s *Store) InitTreeSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS chat_entries (
			id TEXT PRIMARY KEY,
			parent_id TEXT,
			session_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			role TEXT,
			content TEXT,
			msg_type TEXT DEFAULT 'text',
			meta_json TEXT,
			created_at INTEGER,
			FOREIGN KEY(session_id) REFERENCES chat_sessions(id)
		);
		CREATE INDEX IF NOT EXISTS idx_chat_entries_parent ON chat_entries(parent_id);
		CREATE INDEX IF NOT EXISTS idx_chat_entries_session ON chat_entries(session_id);

		CREATE TABLE IF NOT EXISTS chat_lanes (
			session_id TEXT NOT NULL,
			name TEXT NOT NULL,
			head_entry_id TEXT,
			created_at INTEGER,
			updated_at INTEGER,
			PRIMARY KEY(session_id, name),
			FOREIGN KEY(session_id) REFERENCES chat_sessions(id)
		);
	`)
	return err
}

// AppendEntry writes an immutable entry into the DAG and advances the specified lane's head.
func (s *Store) AppendEntry(ctx context.Context, sessionID, laneName string, kind EntryKind, role, content, msgType string, meta map[string]any) (*Entry, error) {
	if sessionID == "" {
		return nil, errors.New("sessions: empty session id")
	}
	if laneName == "" {
		laneName = "main"
	}
	if kind == "" {
		kind = EntryKindMessage
	}

	metaJSON := ""
	if meta != nil {
		if b, err := json.Marshal(meta); err == nil {
			metaJSON = string(b)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().Unix()

	// 1. Get or initialize the lane's current head
	var headID sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT head_entry_id FROM chat_lanes WHERE session_id = ? AND name = ?`, sessionID, laneName).Scan(&headID)
	if errors.Is(err, sql.ErrNoRows) {
		// Initialize lane
		headID = sql.NullString{String: "", Valid: false}
		_, err = tx.ExecContext(ctx, `INSERT INTO chat_lanes (session_id, name, head_entry_id, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			sessionID, laneName, "", now, now)
		if err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}

	parentID := ""
	if headID.Valid {
		parentID = headID.String
	}

	// 2. Insert new entry
	entryID := fmt.Sprintf("entry_%s", uuid.New().String()[:12])
	entry := &Entry{
		ID:        entryID,
		ParentID:  parentID,
		SessionID: sessionID,
		Kind:      kind,
		Role:      role,
		Content:   content,
		MsgType:   msgType,
		MetaJSON:  metaJSON,
		CreatedAt: now,
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO chat_entries (id, parent_id, session_id, kind, role, content, msg_type, meta_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, entry.ID, entry.ParentID, entry.SessionID, string(entry.Kind), entry.Role, entry.Content, entry.MsgType, entry.MetaJSON, entry.CreatedAt)
	if err != nil {
		return nil, err
	}

	// 3. Advance lane head
	_, err = tx.ExecContext(ctx, `
		UPDATE chat_lanes SET head_entry_id = ?, updated_at = ? WHERE session_id = ? AND name = ?
	`, entry.ID, now, sessionID, laneName)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	_ = s.Touch(ctx, sessionID)
	return entry, nil
}

// ForkLane branches a new lane from an existing lane without copying any entries.
func (s *Store) ForkLane(ctx context.Context, sessionID, sourceLane, targetLane string) error {
	if sourceLane == "" {
		sourceLane = "main"
	}
	if targetLane == "" {
		return errors.New("sessions: empty target lane name")
	}

	var headID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT head_entry_id FROM chat_lanes WHERE session_id = ? AND name = ?`, sessionID, sourceLane).Scan(&headID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLaneNotFound
	} else if err != nil {
		return err
	}

	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO chat_lanes (session_id, name, head_entry_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, sessionID, targetLane, headID.String, now, now)

	return err
}

// GetLaneHistory traverses backwards from the lane's head pointer to the root, returning chronological history.
func (s *Store) GetLaneHistory(ctx context.Context, sessionID, laneName string, limit int) ([]Entry, error) {
	if laneName == "" {
		laneName = "main"
	}
	if limit <= 0 {
		limit = 200
	}

	var headID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT head_entry_id FROM chat_lanes WHERE session_id = ? AND name = ?`, sessionID, laneName).Scan(&headID)
	if errors.Is(err, sql.ErrNoRows) {
		return []Entry{}, nil
	} else if err != nil {
		return nil, err
	}

	if !headID.Valid || headID.String == "" {
		return []Entry{}, nil
	}

	// Traverse parent chain backwards
	var entriesReverse []Entry
	currID := headID.String

	for currID != "" && len(entriesReverse) < limit {
		var e Entry
		var parent, meta sql.NullString
		var kindStr string
		err := s.db.QueryRowContext(ctx, `
			SELECT id, parent_id, session_id, kind, role, content, msg_type, meta_json, created_at
			FROM chat_entries WHERE id = ?
		`, currID).Scan(&e.ID, &parent, &e.SessionID, &kindStr, &e.Role, &e.Content, &e.MsgType, &meta, &e.CreatedAt)

		if errors.Is(err, sql.ErrNoRows) {
			break
		} else if err != nil {
			return nil, err
		}

		e.Kind = EntryKind(kindStr)
		if parent.Valid {
			e.ParentID = parent.String
		}
		if meta.Valid {
			e.MetaJSON = meta.String
		}

		entriesReverse = append(entriesReverse, e)
		currID = e.ParentID
	}

	// Reverse to chronological order (root to leaf)
	history := make([]Entry, len(entriesReverse))
	for i, e := range entriesReverse {
		history[len(entriesReverse)-1-i] = e
	}

	return history, nil
}

// ListLanes returns all named lanes for a session.
func (s *Store) ListLanes(ctx context.Context, sessionID string) ([]Lane, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT name, session_id, head_entry_id, created_at, updated_at
		FROM chat_lanes WHERE session_id = ? ORDER BY updated_at DESC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lanes []Lane
	for rows.Next() {
		var l Lane
		var head sql.NullString
		if err := rows.Scan(&l.Name, &l.SessionID, &head, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		if head.Valid {
			l.HeadEntryID = head.String
		}
		lanes = append(lanes, l)
	}
	return lanes, nil
}

// ResetLaneHead rewinds or fast-forwards a lane cursor to an earlier/specific entry.
func (s *Store) ResetLaneHead(ctx context.Context, sessionID, laneName, targetEntryID string) error {
	if laneName == "" {
		laneName = "main"
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE chat_lanes SET head_entry_id = ?, updated_at = ? WHERE session_id = ? AND name = ?
	`, targetEntryID, now, sessionID, laneName)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLaneNotFound
	}
	return nil
}
