package governance

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	agentdb "agentgo/internal/db"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// ApprovalQueue manages thread-safe approval storage in SQLite.
type ApprovalQueue struct {
	db     *sql.DB
	mu     sync.RWMutex
	ownsDB bool
}

// NewApprovalQueue creates a new SQLite-backed ApprovalQueue.
func NewApprovalQueue(dsn string) (*ApprovalQueue, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite DB: %w", err)
	}

	if err := agentdb.Configure(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to init approvals schema: %w", err)
	}

	return &ApprovalQueue{db: db, ownsDB: true}, nil
}

// NewApprovalQueueWithDB reuses an existing pool (e.g. memory.SQLiteStore) to avoid
// a second sql.Open on the same agentgo.db file, which can cause SQLite lock contention.
func NewApprovalQueueWithDB(db *sql.DB) (*ApprovalQueue, error) {
	if db == nil {
		return nil, fmt.Errorf("db is nil")
	}
	if err := initSchema(db); err != nil {
		return nil, fmt.Errorf("failed to init approvals schema: %w", err)
	}
	return &ApprovalQueue{db: db, ownsDB: false}, nil
}

func initSchema(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS approvals (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		interrupt_kind TEXT NOT NULL,
		scope TEXT NOT NULL,
		summary TEXT NOT NULL,
		prompt TEXT NOT NULL,
		metadata TEXT,
		fingerprint TEXT,
		status TEXT NOT NULL DEFAULT 'pending',
		approved INTEGER,
		created_at INTEGER NOT NULL,
		resolved_at INTEGER,
		consumed_at INTEGER,
		resolved_by TEXT,
		resolution_note TEXT,
		labels TEXT,
		policy_tags TEXT,
		resolution_labels TEXT,
		resolution_result TEXT,
		resume_payload TEXT
	);
	CREATE TABLE IF NOT EXISTS governance_audit_log (
		id TEXT PRIMARY KEY,
		timestamp INTEGER NOT NULL,
		channel TEXT NOT NULL,
		session_id TEXT NOT NULL,
		action TEXT NOT NULL,
		tool_name TEXT,
		arguments TEXT,
		result TEXT,
		risk_level TEXT,
		policy_snapshot TEXT,
		user_id TEXT,
		explanation TEXT,
		step_index INTEGER,
		duration_ms INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_gov_audit_session ON governance_audit_log(session_id);
	`
	_, err := db.Exec(query)
	if err != nil {
		return err
	}
	_, _ = db.Exec("ALTER TABLE governance_audit_log ADD COLUMN step_index INTEGER")
	_, _ = db.Exec("ALTER TABLE governance_audit_log ADD COLUMN duration_ms INTEGER")
	return nil
}

// Close closes the underlying SQLite database connection only when this queue opened it.
func (q *ApprovalQueue) Close() error {
	if q == nil || q.db == nil || !q.ownsDB {
		return nil
	}
	return q.db.Close()
}

// CreateRequest saves a new approval request to the queue.
func (q *ApprovalQueue) CreateRequest(ctx context.Context, req *ApprovalRequest) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if req.ID == "" {
		req.ID = fmt.Sprintf("appr_%s", uuid.New().String()[:12])
	}

	metaJSON, _ := json.Marshal(req.Metadata)
	labelsJSON, _ := json.Marshal(req.Labels)
	policyTagsJSON, _ := json.Marshal(req.PolicyTags)
	resLabelsJSON, _ := json.Marshal(req.ResolutionLabels)

	query := `
		INSERT INTO approvals (
			id, kind, interrupt_kind, scope, summary, prompt, metadata, fingerprint, 
			status, approved, created_at, resolved_by, resolution_note, labels, 
			policy_tags, resolution_labels
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	var approvedVal interface{}
	if req.Approved != nil {
		if *req.Approved {
			approvedVal = 1
		} else {
			approvedVal = 0
		}
	}

	_, err := q.db.ExecContext(ctx, query,
		req.ID, req.Kind, string(req.InterruptKind), req.Scope, req.Summary, req.Prompt, string(metaJSON), req.Fingerprint,
		req.Status, approvedVal, req.CreatedAt, req.ResolvedBy, req.ResolutionNote, string(labelsJSON),
		string(policyTagsJSON), string(resLabelsJSON),
	)
	return err
}

// GetRequest retrieves a single approval request by ID.
func (q *ApprovalQueue) GetRequest(ctx context.Context, id string) (*ApprovalRequest, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	query := `
		SELECT id, kind, interrupt_kind, scope, summary, prompt, metadata, fingerprint,
		       status, approved, created_at, resolved_at, consumed_at, resolved_by,
		       resolution_note, labels, policy_tags, resolution_labels, resolution_result, resume_payload
		FROM approvals WHERE id = ?
	`
	row := q.db.QueryRowContext(ctx, query, id)

	var req ApprovalRequest
	var metaStr, labelsStr, tagsStr, resLabelsStr, resResultStr, resumeStr sql.NullString
	var approvedVal sql.NullInt64
	var resolvedAtVal, consumedAtVal sql.NullInt64

	err := row.Scan(
		&req.ID, &req.Kind, &req.InterruptKind, &req.Scope, &req.Summary, &req.Prompt, &metaStr, &req.Fingerprint,
		&req.Status, &approvedVal, &req.CreatedAt, &resolvedAtVal, &consumedAtVal, &req.ResolvedBy,
		&req.ResolutionNote, &labelsStr, &tagsStr, &resLabelsStr, &resResultStr, &resumeStr,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	// Hydrate nullable values
	if approvedVal.Valid {
		b := approvedVal.Int64 == 1
		req.Approved = &b
	}
	if resolvedAtVal.Valid {
		req.ResolvedAt = &resolvedAtVal.Int64
	}
	if consumedAtVal.Valid {
		req.ConsumedAt = &consumedAtVal.Int64
	}

	// Unmarshal JSON fields
	if metaStr.Valid && metaStr.String != "" {
		_ = json.Unmarshal([]byte(metaStr.String), &req.Metadata)
	}
	if labelsStr.Valid && labelsStr.String != "" {
		_ = json.Unmarshal([]byte(labelsStr.String), &req.Labels)
	}
	if tagsStr.Valid && tagsStr.String != "" {
		_ = json.Unmarshal([]byte(tagsStr.String), &req.PolicyTags)
	}
	if resLabelsStr.Valid && resLabelsStr.String != "" {
		_ = json.Unmarshal([]byte(resLabelsStr.String), &req.ResolutionLabels)
	}
	if resResultStr.Valid && resResultStr.String != "" {
		_ = json.Unmarshal([]byte(resResultStr.String), &req.ResolutionResult)
	}
	if resumeStr.Valid && resumeStr.String != "" {
		var resume ResumePayload
		if err := json.Unmarshal([]byte(resumeStr.String), &resume); err == nil {
			req.ResumePayload = &resume
		}
	}

	return &req, nil
}

// ListPending lists all pending approval requests, optionally filtered by kind.
func (q *ApprovalQueue) ListPending(ctx context.Context, kind *string) ([]*ApprovalRequest, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	query := `
		SELECT id, kind, interrupt_kind, scope, summary, prompt, metadata, fingerprint,
		       status, approved, created_at, resolved_at, consumed_at, resolved_by,
		       resolution_note, labels, policy_tags, resolution_labels, resolution_result, resume_payload
		FROM approvals WHERE status = 'pending'
	`
	var args []interface{}
	if kind != nil {
		query += " AND kind = ?"
		args = append(args, *kind)
	}
	query += " ORDER BY created_at DESC"

	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []*ApprovalRequest
	for rows.Next() {
		var req ApprovalRequest
		var metaStr, labelsStr, tagsStr, resLabelsStr, resResultStr, resumeStr sql.NullString
		var approvedVal sql.NullInt64
		var resolvedAtVal, consumedAtVal sql.NullInt64

		err := rows.Scan(
			&req.ID, &req.Kind, &req.InterruptKind, &req.Scope, &req.Summary, &req.Prompt, &metaStr, &req.Fingerprint,
			&req.Status, &approvedVal, &req.CreatedAt, &resolvedAtVal, &consumedAtVal, &req.ResolvedBy,
			&req.ResolutionNote, &labelsStr, &tagsStr, &resLabelsStr, &resResultStr, &resumeStr,
		)
		if err != nil {
			return nil, err
		}

		if approvedVal.Valid {
			b := approvedVal.Int64 == 1
			req.Approved = &b
		}
		if resolvedAtVal.Valid {
			req.ResolvedAt = &resolvedAtVal.Int64
		}
		if consumedAtVal.Valid {
			req.ConsumedAt = &consumedAtVal.Int64
		}

		if metaStr.Valid && metaStr.String != "" {
			_ = json.Unmarshal([]byte(metaStr.String), &req.Metadata)
		}
		if labelsStr.Valid && labelsStr.String != "" {
			_ = json.Unmarshal([]byte(labelsStr.String), &req.Labels)
		}
		if tagsStr.Valid && tagsStr.String != "" {
			_ = json.Unmarshal([]byte(tagsStr.String), &req.PolicyTags)
		}
		if resLabelsStr.Valid && resLabelsStr.String != "" {
			_ = json.Unmarshal([]byte(resLabelsStr.String), &req.ResolutionLabels)
		}
		if resResultStr.Valid && resResultStr.String != "" {
			_ = json.Unmarshal([]byte(resResultStr.String), &req.ResolutionResult)
		}
		if resumeStr.Valid && resumeStr.String != "" {
			var resume ResumePayload
			if err := json.Unmarshal([]byte(resumeStr.String), &resume); err == nil {
				req.ResumePayload = &resume
			}
		}

		requests = append(requests, &req)
	}

	return requests, nil
}

// Resolve marks a pending request as approved or rejected and updates the resume payload.
func (q *ApprovalQueue) Resolve(ctx context.Context, id string, approved bool, note string, resolvedBy string, resume *ResumePayload) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	status := "rejected"
	if approved {
		status = "approved"
	}

	approvedVal := 0
	if approved {
		approvedVal = 1
	}

	now := time.Now().Unix()

	var resumeJSON []byte
	if resume != nil {
		resumeJSON, _ = json.Marshal(resume)
	}

	query := `
		UPDATE approvals
		SET status = ?, approved = ?, resolved_at = ?, resolved_by = ?, resolution_note = ?, resume_payload = ?
		WHERE id = ? AND status = 'pending'
	`
	res, err := q.db.ExecContext(ctx, query, status, approvedVal, now, resolvedBy, note, string(resumeJSON), id)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("approval request %s not found or already resolved", id)
	}

	return nil
}

// ExpirePendingOlderThan marks stale pending approvals as expired (delegation timeout escalation).
func (q *ApprovalQueue) ExpirePendingOlderThan(ctx context.Context, maxAge time.Duration) (int64, error) {
	if q == nil || maxAge <= 0 {
		return 0, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	cutoff := time.Now().Add(-maxAge).Unix()
	res, err := q.db.ExecContext(ctx, `
		UPDATE approvals SET status = 'expired', resolved_at = ?, resolution_note = ?
		WHERE status = 'pending' AND created_at < ?
	`, time.Now().Unix(), "auto-expired: delegation timeout", cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Consume marks an approved request as consumed.
func (q *ApprovalQueue) Consume(ctx context.Context, id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now().Unix()
	query := `
		UPDATE approvals
		SET consumed_at = ?
		WHERE id = ? AND status = 'approved' AND consumed_at IS NULL
	`
	res, err := q.db.ExecContext(ctx, query, now, id)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("approval request %s not found, not approved, or already consumed", id)
	}

	return nil
}

func (q *ApprovalQueue) findApprovedUnconsumed(ctx context.Context, fingerprint string) (string, bool) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id FROM approvals WHERE fingerprint = ? AND status = 'approved' AND consumed_at IS NULL LIMIT 1`,
		fingerprint)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", false
	}
	return id, true
}

func (q *ApprovalQueue) findPending(ctx context.Context, fingerprint string) (string, bool) {
	row := q.db.QueryRowContext(ctx,
		`SELECT id FROM approvals WHERE fingerprint = ? AND status = 'pending' LIMIT 1`,
		fingerprint)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", false
	}
	return id, true
}
