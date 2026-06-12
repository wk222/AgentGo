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
	`)
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.title, s.created_at, s.updated_at,
			(SELECT COUNT(*) FROM chat_messages m WHERE m.session_id = s.id) AS msg_count
		FROM chat_sessions s ORDER BY s.updated_at DESC LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ID, &x.Title, &x.CreatedAt, &x.UpdatedAt, &x.MessageCount); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
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
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("empty session id")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM chat_messages WHERE session_id = ?`, sessionID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM chat_sessions WHERE id = ?`, sessionID)
	return err
}

func (s *Store) GetMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, msg_type, meta_json, created_at
		FROM chat_messages WHERE session_id = ? ORDER BY created_at ASC LIMIT ?
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
	return out, nil
}
