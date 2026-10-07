package sessions

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunEvent is one durable fact about a run in a session: a tool call and its
// result, a file change, an approval request, an error, and the commit point.
// Events are append-only and numbered per session, so a client that reconnects
// asks for everything after the last Seq it saw.
//
// Transient streaming (text and reasoning deltas) is not stored here; the final
// assistant message is stored in chat_messages and the Done event below marks
// that it has been committed.
type RunEvent struct {
	SessionID   string `json:"session_id"`
	Seq         int64  `json:"seq"`
	RunID       string `json:"run_id"`
	Kind        string `json:"kind"`
	CallID      string `json:"call_id,omitempty"`
	Name        string `json:"name,omitempty"`
	PayloadJSON string `json:"payload_json,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

// Kinds recorded by the bridge. Done is the only commit point: it is appended
// after the run's final messages are in chat_messages.
const (
	KindStatus      = "status"
	KindToolCall    = "tool_call"
	KindToolResult  = "tool_result"
	KindFileChange  = "file_change"
	KindApproval    = "approval_request"
	KindError       = "error"
	KindDone        = "done"
	maxRunEventList = 1000
)

const runEventsSchema = `
CREATE TABLE IF NOT EXISTS chat_run_events (
	session_id   TEXT NOT NULL,
	seq          INTEGER NOT NULL,
	run_id       TEXT NOT NULL,
	kind         TEXT NOT NULL,
	call_id      TEXT,
	name         TEXT,
	payload_json TEXT,
	created_at   INTEGER,
	PRIMARY KEY (session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_chat_run_events_run ON chat_run_events(session_id, run_id);
`

// AppendRunEvent stores e and returns its per-session sequence number. The
// number is assigned inside a single statement, so concurrent appends to one
// session never collide or reuse a number.
func (s *Store) AppendRunEvent(ctx context.Context, e RunEvent) (int64, error) {
	if strings.TrimSpace(e.SessionID) == "" {
		return 0, errors.New("sessions: run event without a session id")
	}
	if e.Kind == "" || e.RunID == "" {
		return 0, errors.New("sessions: run event needs kind and run id")
	}
	var seq int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO chat_run_events (session_id, seq, run_id, kind, call_id, name, payload_json, created_at)
		SELECT ?, COALESCE(MAX(seq), 0) + 1, ?, ?, ?, ?, ?, ? FROM chat_run_events WHERE session_id = ?
		RETURNING seq
	`, e.SessionID, e.RunID, e.Kind, e.CallID, e.Name, e.PayloadJSON, time.Now().Unix(), e.SessionID).Scan(&seq)
	return seq, err
}

// ListRunEvents returns events with Seq > afterSeq in order, at most limit.
func (s *Store) ListRunEvents(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]RunEvent, error) {
	if limit <= 0 || limit > maxRunEventList {
		limit = maxRunEventList
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, seq, run_id, kind, COALESCE(call_id,''), COALESCE(name,''), COALESCE(payload_json,''), COALESCE(created_at,0)
		FROM chat_run_events WHERE session_id = ? AND seq > ? ORDER BY seq ASC LIMIT ?
	`, sessionID, afterSeq, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunEvent
	for rows.Next() {
		var e RunEvent
		if err := rows.Scan(&e.SessionID, &e.Seq, &e.RunID, &e.Kind, &e.CallID, &e.Name, &e.PayloadJSON, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RunStatus is derived from the log, never stored separately.
type RunStatus string

const (
	// RunUnfinished: events exist but the commit point was never written. If no
	// live run owns it (for example after a crash) it was interrupted.
	RunUnfinished       RunStatus = "unfinished"
	RunAwaitingApproval RunStatus = "awaiting_approval"
	RunCompleted        RunStatus = "completed"
	RunFailed           RunStatus = "failed"
	RunNone             RunStatus = "none"
)

// RunState describes the most recent run of a session.
type RunState struct {
	RunID  string    `json:"run_id,omitempty"`
	Status RunStatus `json:"status"`
	Error  string    `json:"error,omitempty"`
	Seq    int64     `json:"seq"` // last event number of the session
}

// LastRunState derives the state of the session's latest run from its events.
// A run is unfinished until its Done event exists; Done carries the outcome.
func (s *Store) LastRunState(ctx context.Context, sessionID string) (RunState, error) {
	var last RunEvent
	var payload sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT seq, run_id, kind, payload_json FROM chat_run_events WHERE session_id = ? ORDER BY seq DESC LIMIT 1
	`, sessionID).Scan(&last.Seq, &last.RunID, &last.Kind, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return RunState{Status: RunNone}, nil
	}
	if err != nil {
		return RunState{}, err
	}
	st := RunState{RunID: last.RunID, Seq: last.Seq, Status: RunUnfinished}
	// The latest event decides. A run that paused commits as pending and then
	// carries on when resumed, so a commit followed by more events is unfinished.
	if last.Kind != KindDone {
		return st, nil
	}
	var done struct {
		Error   string `json:"error"`
		Pending bool   `json:"pending"`
	}
	if payload.Valid && payload.String != "" {
		if err := json.Unmarshal([]byte(payload.String), &done); err != nil {
			return RunState{}, fmt.Errorf("sessions: bad done payload: %w", err)
		}
	}
	switch {
	case done.Pending:
		st.Status = RunAwaitingApproval
	case done.Error != "":
		st.Status, st.Error = RunFailed, done.Error
	default:
		st.Status = RunCompleted
	}
	return st, nil
}
