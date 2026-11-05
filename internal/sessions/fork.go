package sessions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// chat_session_forks remembers where a forked conversation came from. It has no
// foreign key on parent_id on purpose: deleting the original must leave the
// copy, and its title for the lineage line, alone.
const sessionForksSchema = `
CREATE TABLE IF NOT EXISTS chat_session_forks (
	session_id   TEXT PRIMARY KEY,
	parent_id    TEXT NOT NULL,
	parent_title TEXT NOT NULL DEFAULT '',
	forked_at    INTEGER NOT NULL,
	messages     INTEGER NOT NULL DEFAULT 0,
	events       INTEGER NOT NULL DEFAULT 0
);`

// ForkResult describes a finished fork.
type ForkResult struct {
	Session     Session `json:"session"`
	ParentID    string  `json:"parent_id"`
	ParentTitle string  `json:"parent_title"`
	Messages    int     `json:"messages"`
	Events      int     `json:"events"`
	// Mode is true when the original had its own mode and the copy inherited it.
	Mode bool `json:"mode"`
}

const (
	// The longest marker is "（分叉 99）" (7 runes), so base + marker stays inside
	// MaxTitleRunes and the sidebar's rename box can still hold the title.
	forkBaseMaxRunes    = 60
	forkTitleAttempts   = 99
	forkDefaultBaseName = "新对话"
)

// forkSuffix matches the marker ForkSession adds to a default title, so
// forking a fork does not stack "（分叉）（分叉）".
var forkSuffix = regexp.MustCompile(`（分叉(?: ?\d+)?）$`)

// ForkSession copies a whole conversation into a new session: the transcript,
// the model's own context log (every workspace scope) and the session mode. The
// copy is one transaction, so a failure leaves neither a half-filled session nor
// a lineage row behind. Files outside the database (plan tasks), memory and run
// logs are not touched here; callers that own them copy them afterwards.
//
// Callers must make sure the original is idle: a run in flight keeps appending
// to the original while the copy is taken.
func (s *Store) ForkSession(ctx context.Context, sourceID, title string) (ForkResult, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return ForkResult{}, errors.New("empty session id")
	}
	// The pool may hold a single connection (see db.Configure) that the
	// transaction below owns, so everything that needs the pool happens first.
	var parentTitle string
	if err := s.db.QueryRowContext(ctx, `SELECT title FROM chat_sessions WHERE id = ?`, sourceID).Scan(&parentTitle); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ForkResult{}, ErrNotFound
		}
		return ForkResult{}, err
	}
	if title = CleanTitle(title); title == "" {
		title = s.freeForkTitle(ctx, parentTitle)
	}
	// A copy keeps working as the same expert, just as it keeps the mode.
	expertID, err := s.Expert(ctx, sourceID)
	if err != nil {
		return ForkResult{}, fmt.Errorf("fork expert: %w", err)
	}

	now := time.Now().Unix()
	out := ForkResult{
		ParentID:    sourceID,
		ParentTitle: parentTitle,
		Session: Session{
			ID:          fmt.Sprintf("sess_%s", uuid.New().String()[:12]),
			Title:       title,
			CreatedAt:   now,
			UpdatedAt:   now,
			ParentID:    sourceID,
			ParentTitle: parentTitle,
			ExpertID:    expertID,
		},
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ForkResult{}, err
	}
	defer tx.Rollback()

	// The original's row is also the guard: a session deleted since the read
	// above inserts nothing, and the first statement being a write keeps the
	// transaction from upgrading a read lock later.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO chat_sessions (id, title, created_at, updated_at)
		SELECT ?, ?, ?, ? WHERE EXISTS (SELECT 1 FROM chat_sessions WHERE id = ?)
	`, out.Session.ID, out.Session.Title, now, now, sourceID)
	if err != nil {
		return ForkResult{}, fmt.Errorf("fork session: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ForkResult{}, ErrNotFound
	}

	// ORDER BY makes the copy take rowids in the original's reading order, which
	// is what GetMessages sorts by after created_at.
	res, err = tx.ExecContext(ctx, `
		INSERT INTO chat_messages (id, session_id, role, content, msg_type, meta_json, created_at)
		SELECT 'msg_' || lower(hex(randomblob(8))), ?, role, content, msg_type, meta_json, created_at
		FROM chat_messages WHERE session_id = ? ORDER BY created_at, rowid
	`, out.Session.ID, sourceID)
	if err != nil {
		return ForkResult{}, fmt.Errorf("fork messages: %w", err)
	}
	out.Messages = rowsAffected(res)

	// The payloads never name their session; the (session, scope, seq) key is
	// the only place the id lives, so a verbatim copy is a valid log.
	res, err = tx.ExecContext(ctx, `
		INSERT INTO chat_context_events (session_id, scope, seq, event_id, kind, payload)
		SELECT ?, scope, seq, event_id, kind, payload FROM chat_context_events WHERE session_id = ?
	`, out.Session.ID, sourceID)
	if err != nil {
		return ForkResult{}, fmt.Errorf("fork context events: %w", err)
	}
	out.Events = rowsAffected(res)

	res, err = tx.ExecContext(ctx, `
		INSERT INTO session_modes (session_id, profile, canvas, updated_at)
		SELECT ?, profile, canvas, ? FROM session_modes WHERE session_id = ?
	`, out.Session.ID, now, sourceID)
	if err != nil {
		return ForkResult{}, fmt.Errorf("fork mode: %w", err)
	}
	out.Mode = rowsAffected(res) > 0

	if expertID != "" {
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO chat_session_experts (session_id, expert_id, bound_at) VALUES (?, ?, ?)
		`, out.Session.ID, expertID, now); err != nil {
			return ForkResult{}, fmt.Errorf("fork expert: %w", err)
		}
	}

	if _, err = tx.ExecContext(ctx, `
		INSERT INTO chat_session_forks (session_id, parent_id, parent_title, forked_at, messages, events)
		VALUES (?, ?, ?, ?, ?, ?)
	`, out.Session.ID, sourceID, parentTitle, now, out.Messages, out.Events); err != nil {
		return ForkResult{}, fmt.Errorf("fork lineage: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return ForkResult{}, err
	}
	out.Session.MessageCount = out.Messages
	return out, nil
}

func rowsAffected(res sql.Result) int {
	n, err := res.RowsAffected()
	if err != nil || n < 0 {
		return 0
	}
	return int(n)
}

// freeForkTitle picks "<title>（分叉）", then "（分叉 2）", … so a few forks of
// one conversation stay tellable apart in the sidebar. A lookup failure only
// costs the numbering.
func (s *Store) freeForkTitle(ctx context.Context, parentTitle string) string {
	base := forkBaseTitle(parentTitle)
	candidate := base + "（分叉）"
	for n := 1; n <= forkTitleAttempts; n++ {
		if n > 1 {
			candidate = fmt.Sprintf("%s（分叉 %d）", base, n)
		}
		var taken int
		err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM chat_sessions WHERE title = ?)`, candidate).Scan(&taken)
		if err != nil || taken == 0 {
			return candidate
		}
	}
	return candidate
}

func forkBaseTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" || title == forkDefaultBaseName || strings.EqualFold(title, "new chat") {
		return forkDefaultBaseName
	}
	title = strings.TrimSpace(forkSuffix.ReplaceAllString(title, ""))
	if title == "" {
		return forkDefaultBaseName
	}
	return clipRunes(title, forkBaseMaxRunes)
}
