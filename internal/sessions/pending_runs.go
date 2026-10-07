package sessions

import (
	"context"
	"errors"
	"strings"
	"time"
)

// PendingRun is a run that stopped for a decision (approval or an answer) and
// can be resumed later. It is stored so that it survives a restart: the agent's
// checkpoint is already durable, and without this row nothing would know which
// checkpoint belongs to which approval.
//
// Several processes (desktop, TUI, headless) share one database, so each row
// records who owns it. A process only adopts rows whose owner is gone.
type PendingRun struct {
	ApprovalID  string `json:"approval_id"`
	SessionID   string `json:"session_id"`
	OwnerPID    int    `json:"owner_pid"`
	PayloadJSON string `json:"payload_json"` // the bridge's description of how to resume
	CreatedAt   int64  `json:"created_at"`
}

const pendingRunsSchema = `
CREATE TABLE IF NOT EXISTS pending_runs (
	approval_id  TEXT PRIMARY KEY,
	session_id   TEXT NOT NULL,
	owner_pid    INTEGER NOT NULL,
	payload_json TEXT NOT NULL,
	created_at   INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pending_runs_session ON pending_runs(session_id);
`

// SavePendingRun inserts or replaces a pending run.
func (s *Store) SavePendingRun(ctx context.Context, p PendingRun) error {
	if strings.TrimSpace(p.ApprovalID) == "" {
		return errors.New("sessions: pending run without an approval id")
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pending_runs (approval_id, session_id, owner_pid, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(approval_id) DO UPDATE SET
			session_id = excluded.session_id,
			owner_pid = excluded.owner_pid,
			payload_json = excluded.payload_json
	`, p.ApprovalID, p.SessionID, p.OwnerPID, p.PayloadJSON, p.CreatedAt)
	return err
}

// DeletePendingRun removes a pending run; a missing row is not an error.
func (s *Store) DeletePendingRun(ctx context.Context, approvalID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pending_runs WHERE approval_id = ?`, approvalID)
	return err
}

// ListPendingRuns returns every stored pending run, oldest first.
func (s *Store) ListPendingRuns(ctx context.Context) ([]PendingRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT approval_id, session_id, owner_pid, payload_json, created_at
		FROM pending_runs ORDER BY created_at ASC, approval_id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingRun
	for rows.Next() {
		var p PendingRun
		if err := rows.Scan(&p.ApprovalID, &p.SessionID, &p.OwnerPID, &p.PayloadJSON, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AwaitingRun names a session whose latest run is paused for approval.
type AwaitingRun struct {
	SessionID string
	RunID     string
}

// ListAwaitingRuns returns the sessions whose latest log event is a commit that
// says the run is waiting for a decision. Used on startup to find paused runs
// that no pending run refers to any more.
func (s *Store) ListAwaitingRuns(ctx context.Context) ([]AwaitingRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.session_id, e.run_id
		FROM chat_run_events e
		JOIN (SELECT session_id, MAX(seq) AS m FROM chat_run_events GROUP BY session_id) l
		  ON e.session_id = l.session_id AND e.seq = l.m
		WHERE e.kind = ? AND json_extract(e.payload_json, '$.pending') = 1
		ORDER BY e.session_id
	`, KindDone)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AwaitingRun
	for rows.Next() {
		var a AwaitingRun
		if err := rows.Scan(&a.SessionID, &a.RunID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
