package docindex

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// A sync follows one directory of a workspace through possibly many calls. Its state lives here so a large
// folder can be walked in steps (the cursor), and so a file is only taken as deleted after one complete,
// error-free walk has failed to see it (the seen set). Every state change names the scan it belongs to: a
// late call from an older scan cannot move the cursor or add to the seen set of a newer one.

const syncSchema = `
CREATE TABLE IF NOT EXISTS doc_index_syncs (
	scope TEXT NOT NULL,
	root TEXT NOT NULL,
	scan_id TEXT NOT NULL,
	cursor TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL,
	incomplete INTEGER NOT NULL DEFAULT 0,
	visited INTEGER NOT NULL DEFAULT 0,
	started_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	PRIMARY KEY (scope, root)
);
CREATE TABLE IF NOT EXISTS doc_index_sync_seen (
	scope TEXT NOT NULL,
	root TEXT NOT NULL,
	scan_id TEXT NOT NULL,
	source TEXT NOT NULL,
	PRIMARY KEY (scope, root, scan_id, source)
);
`

// Scan states.
const (
	SyncRunning    = "running"    // walked part of the folder; the next call continues after Cursor
	SyncComplete   = "complete"   // the whole folder was walked without error
	SyncIncomplete = "incomplete" // the walk finished but something could not be read; nothing was treated as deleted
)

// ErrStaleScan means the scan a call belongs to was replaced by a newer one.
var ErrStaleScan = errors.New("document index: this scan was replaced by a newer one")

// Sync is the state of the latest scan of one folder.
type Sync struct {
	Scope      string `json:"scope"`
	Root       string `json:"root"` // workspace-relative folder, "." for the workspace itself
	ScanID     string `json:"scan_id"`
	Cursor     string `json:"cursor"` // last source handled, in walk order; "" before the first
	Status     string `json:"status"`
	Incomplete bool   `json:"incomplete"`
	Visited    int    `json:"visited"`
	StartedAt  int64  `json:"started_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

func (s *Store) syncOpen() (*sql.DB, error) {
	db, err := s.open()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := db.Exec(syncSchema); err != nil {
		return nil, err
	}
	return db, nil
}

// SyncGet returns the latest scan of a folder.
func (s *Store) SyncGet(ctx context.Context, scope, root string) (Sync, bool, error) {
	db, err := s.syncOpen()
	if err != nil {
		return Sync{}, false, err
	}
	var st Sync
	var inc int
	err = db.QueryRowContext(ctx, `SELECT scope,root,scan_id,cursor,status,incomplete,visited,started_at,updated_at FROM doc_index_syncs WHERE scope=? AND root=?`, scope, root).
		Scan(&st.Scope, &st.Root, &st.ScanID, &st.Cursor, &st.Status, &inc, &st.Visited, &st.StartedAt, &st.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Sync{}, false, nil
	}
	st.Incomplete = inc != 0
	return st, err == nil, err
}

// SyncBegin starts a new scan of a folder, replacing any earlier one and forgetting what it had seen.
func (s *Store) SyncBegin(ctx context.Context, scope, root string) (Sync, error) {
	db, err := s.syncOpen()
	if err != nil {
		return Sync{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().Unix()
	st := Sync{Scope: scope, Root: root, ScanID: uuid.NewString(), Status: SyncRunning, StartedAt: now, UpdatedAt: now}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Sync{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_index_sync_seen WHERE scope=? AND root=?`, scope, root); err != nil {
		return Sync{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO doc_index_syncs(scope,root,scan_id,cursor,status,incomplete,visited,started_at,updated_at) VALUES(?,?,?,'',?,0,0,?,?)
ON CONFLICT(scope,root) DO UPDATE SET scan_id=excluded.scan_id,cursor='',status=excluded.status,incomplete=0,visited=0,started_at=excluded.started_at,updated_at=excluded.updated_at`,
		scope, root, st.ScanID, SyncRunning, now, now); err != nil {
		return Sync{}, err
	}
	return st, tx.Commit()
}

// SyncProgress records that the walk handled seen (in order) and now stands at cursor. incomplete is sticky: once a
// scan met something it could not read, it stays incomplete. It fails with ErrStaleScan when scanID is not current.
func (s *Store) SyncProgress(ctx context.Context, scope, root, scanID, cursor string, seen []string, visited int, incomplete bool) error {
	db, err := s.syncOpen()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE doc_index_syncs SET cursor=?, visited=visited+?, incomplete=MAX(incomplete,?), updated_at=? WHERE scope=? AND root=? AND scan_id=? AND status=?`,
		cursor, visited, boolInt(incomplete), time.Now().Unix(), scope, root, scanID, SyncRunning)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleScan
	}
	for _, src := range seen {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO doc_index_sync_seen(scope,root,scan_id,source) VALUES(?,?,?,?)`, scope, root, scanID, src); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SyncUnseen lists the indexed sources inside root that the scan did not see. Only meaningful for a scan that walked
// the whole folder; the caller checks that first.
func (s *Store) SyncUnseen(ctx context.Context, scope, root, scanID string) ([]string, error) {
	db, err := s.syncOpen()
	if err != nil {
		return nil, err
	}
	q := `SELECT f.source FROM doc_index_files f WHERE f.scope=? AND NOT EXISTS (
SELECT 1 FROM doc_index_sync_seen s WHERE s.scope=f.scope AND s.root=? AND s.scan_id=? AND s.source=f.source)`
	args := []any{scope, root, scanID}
	if clause, extra := prefixClause("f.source", root); clause != "" {
		q += " AND " + clause
		args = append(args, extra...)
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY f.source`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var src string
		if err := rows.Scan(&src); err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// SyncFinish ends a scan with status complete or incomplete and drops its seen set. It fails with ErrStaleScan when
// scanID is not current.
func (s *Store) SyncFinish(ctx context.Context, scope, root, scanID, status string) error {
	if status != SyncComplete && status != SyncIncomplete {
		return errors.New("document index: finish status must be complete or incomplete")
	}
	db, err := s.syncOpen()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE doc_index_syncs SET status=?, updated_at=? WHERE scope=? AND root=? AND scan_id=? AND status=?`,
		status, time.Now().Unix(), scope, root, scanID, SyncRunning)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrStaleScan
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM doc_index_sync_seen WHERE scope=? AND root=? AND scan_id=?`, scope, root, scanID); err != nil {
		return err
	}
	return tx.Commit()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// NormSyncRoot makes the folder key the same however the caller wrote it.
func NormSyncRoot(root string) string {
	root = strings.Trim(strings.ReplaceAll(strings.TrimSpace(root), "\\", "/"), "/")
	if root == "" {
		return "."
	}
	return root
}
