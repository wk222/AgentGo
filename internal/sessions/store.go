package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Session struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	MessageCount int    `json:"message_count"`
	// ParentID/ParentTitle name the conversation this one was forked from (see
	// ForkSession); ParentGone is set once that original has been deleted, in
	// which case ParentTitle is the title it had at fork time.
	ParentID    string `json:"parent_id,omitempty"`
	ParentTitle string `json:"parent_title,omitempty"`
	ParentGone  bool   `json:"parent_gone,omitempty"`
}

type Message struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Type      string `json:"type"`
	MetaJSON  string `json:"meta_json,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS chat_sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			created_at INTEGER,
			updated_at INTEGER
		);
		CREATE TABLE IF NOT EXISTS chat_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT,
			msg_type TEXT DEFAULT 'text',
			meta_json TEXT,
			created_at INTEGER,
			FOREIGN KEY(session_id) REFERENCES chat_sessions(id)
		);
		CREATE INDEX IF NOT EXISTS idx_chat_messages_session ON chat_messages(session_id);

		-- chat_entries and chat_lanes are legacy conversation-tree tables. Nothing writes them any
		-- more because the model context lives in chat_context_events. They stay so existing
		-- databases keep valid foreign keys and session deletion still clears old rows. Drop them
		-- in a future migration once no database has rows left.
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
	if err != nil {
		return err
	}
	if _, err = db.Exec(runEventsSchema); err != nil {
		return err
	}
	if _, err = db.Exec(sessionModesSchema); err != nil {
		return err
	}
	if _, err = db.Exec(contextEventsSchema); err != nil {
		return err
	}
	if _, err = db.Exec(sessionForksSchema); err != nil {
		return err
	}
	_, err = db.Exec(pendingRunsSchema)
	return err
}

func Open(db *sql.DB) (*Store, error) {
	if err := NewStore(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) List(ctx context.Context, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 100
	}
	// The original's live title wins over the one recorded at fork time, so a
	// rename shows up in the lineage line; once it is gone the record is all
	// that is left.
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.title, s.created_at, s.updated_at,
			(SELECT COUNT(*) FROM chat_messages m WHERE m.session_id = s.id) AS msg_count,
			COALESCE(f.parent_id, ''),
			COALESCE((SELECT p.title FROM chat_sessions p WHERE p.id = f.parent_id), f.parent_title, ''),
			CASE WHEN f.parent_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM chat_sessions p WHERE p.id = f.parent_id) THEN 1 ELSE 0 END
		FROM chat_sessions s LEFT JOIN chat_session_forks f ON f.session_id = s.id
		ORDER BY s.updated_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		var gone int
		if err := rows.Scan(&x.ID, &x.Title, &x.CreatedAt, &x.UpdatedAt, &x.MessageCount, &x.ParentID, &x.ParentTitle, &gone); err != nil {
			return nil, err
		}
		x.ParentGone = gone != 0
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, title string) (Session, error) {
	now := time.Now().Unix()
	if title == "" {
		title = "New chat"
	}
	sess := Session{
		ID:        fmt.Sprintf("sess_%s", uuid.New().String()[:12]),
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_sessions (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)
	`, sess.ID, sess.Title, sess.CreatedAt, sess.UpdatedAt)
	return sess, err
}

// EnsureSession creates the session row when it does not exist yet. Callers that
// receive a session id from outside (HTTP gateway, ACP) use it so message writes
// do not fail on the chat_sessions foreign key.
func (s *Store) EnsureSession(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("empty session id")
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO chat_sessions (id, title, created_at, updated_at) VALUES (?, ?, ?, ?)
	`, id, "New chat", now, now)
	return err
}

func (s *Store) Touch(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE chat_sessions SET updated_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}

// AutoTitleFromUserMessage renames default-titled sessions from the first user line.
func (s *Store) AutoTitleFromUserMessage(ctx context.Context, sessionID, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	var title string
	if err := s.db.QueryRowContext(ctx, `SELECT title FROM chat_sessions WHERE id = ?`, sessionID).Scan(&title); err != nil {
		return err
	}
	if title != "" && title != "新对话" && title != "New chat" {
		return nil
	}
	if utf8.RuneCountInString(content) > 24 {
		content = string([]rune(content)[:24]) + "…"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE chat_sessions SET title = ?, updated_at = ? WHERE id = ?`,
		content, time.Now().Unix(), sessionID)
	return err
}

func (s *Store) AppendMessage(ctx context.Context, sessionID, role, content, msgType string, meta map[string]any) error {
	metaJSON := ""
	if meta != nil {
		b, _ := json.Marshal(meta)
		metaJSON = string(b)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_messages (id, session_id, role, content, msg_type, meta_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, fmt.Sprintf("msg_%s", uuid.New().String()[:12]), sessionID, role, content, msgType, metaJSON, time.Now().Unix())
	if err == nil {
		_ = s.Touch(ctx, sessionID)
	}
	return err
}

// Delete removes a session and all its messages.
func (s *Store) Delete(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("empty session id")
	}
	_, err := s.DeleteBatch(ctx, []string{sessionID})
	return err
}

// deleteSessionRows runs inside the caller's transaction; suffix is a fixed SQL
// predicate supplied only by the methods below. Delete children before parents.
func deleteSessionRows(ctx context.Context, tx *sql.Tx, suffix string, args ...any) (int, error) {
	for _, table := range []string{"chat_lanes", "chat_entries", "chat_messages", "chat_context_events", "chat_run_events", "chat_session_forks", "pending_runs", "session_modes"} {
		query := "DELETE FROM " + table + " WHERE session_id" + suffix
		values := append([]any(nil), args...)
		if table == "session_modes" {
			query += " AND session_id != ?"
			values = append(values, DefaultModeKey)
		}
		if _, err := tx.ExecContext(ctx, query, values...); err != nil {
			return 0, fmt.Errorf("delete %s: %w", table, err)
		}
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM chat_sessions WHERE id"+suffix, args...)
	if err != nil {
		return 0, err
	}
	count, err := result.RowsAffected()
	return int(count), err
}

// DeleteBatch atomically removes transcripts, native events, tree branches,
// modes and pending approvals. Any failed delete rolls back the entire batch.
func (s *Store) DeleteBatch(ctx context.Context, sessionIDs []string) (int, error) {
	var clean []string
	seen := map[string]bool{}
	for _, raw := range sessionIDs {
		id := strings.TrimSpace(raw)
		if id != "" && !seen[id] {
			seen[id] = true
			clean = append(clean, id)
		}
	}
	if len(clean) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	deleted := 0
	for _, id := range clean {
		n, err := deleteSessionRows(ctx, tx, " = ?", id)
		if err != nil {
			return 0, err
		}
		deleted += n
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return deleted, nil
}

// DeleteAllExcept preserves the runner-wide default mode when clearing chats.
func (s *Store) DeleteAllExcept(ctx context.Context, keepSessionID string) (int, error) {
	keepSessionID = strings.TrimSpace(keepSessionID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	suffix := " IS NOT NULL"
	var args []any
	if keepSessionID != "" {
		suffix = " != ?"
		args = []any{keepSessionID}
	}
	n, err := deleteSessionRows(ctx, tx, suffix, args...)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return n, nil
}

// DeleteByTitle removes all sessions matching the given title across the entire database.
func (s *Store) DeleteByTitle(ctx context.Context, title string, keepSessionID string) (int, error) {
	title = strings.TrimSpace(title)
	keepSessionID = strings.TrimSpace(keepSessionID)
	if title == "" {
		return 0, fmt.Errorf("empty title")
	}

	var rows *sql.Rows
	var err error
	if title == "新对话" || strings.EqualFold(title, "new chat") {
		rows, err = s.db.QueryContext(ctx, `SELECT id FROM chat_sessions WHERE (TRIM(title) = '' OR TRIM(title) = '新对话' OR LOWER(TRIM(title)) = 'new chat') AND id != ?`, keepSessionID)
	} else {
		rows, err = s.db.QueryContext(ctx, `SELECT id FROM chat_sessions WHERE TRIM(title) = ? AND id != ?`, title, keepSessionID)
	}
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	readErr := rows.Err()
	_ = rows.Close()
	if readErr != nil {
		return 0, readErr
	}
	return s.DeleteBatch(ctx, ids)
}

func (s *Store) GetMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	// The newest `limit` messages, oldest first. Ordering by created_at alone
	// (whole seconds) cannot separate a user message from the reply written in
	// the same second, so rowid breaks ties in insertion order. Taking the
	// oldest `limit` instead would freeze long sessions: later turns would
	// never reach the model's history.
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, msg_type, meta_json, created_at FROM (
			SELECT rowid AS rid, id, session_id, role, content, msg_type, meta_json, created_at
			FROM chat_messages WHERE session_id = ? ORDER BY created_at DESC, rowid DESC LIMIT ?
		) ORDER BY created_at ASC, rid ASC
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var meta sql.NullString
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Type, &meta, &m.CreatedAt); err != nil {
			return nil, err
		}
		if meta.Valid {
			m.MetaJSON = meta.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
