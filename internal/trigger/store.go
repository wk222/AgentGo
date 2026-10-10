package trigger

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Store persists triggers so they survive restarts and are shared by every process on the same DB.
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) (*Store, error) {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS triggers (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			title TEXT,
			pid INTEGER,
			path TEXT,
			pattern TEXT,
			action TEXT NOT NULL,
			session_id TEXT,
			prompt TEXT,
			run_id TEXT,
			signal TEXT,
			status TEXT NOT NULL,
			created_at INTEGER,
			expires_at INTEGER,
			fired_at INTEGER,
			delivered INTEGER DEFAULT 0,
			result TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_triggers_status ON triggers(status);
	`)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

const cols = `id, kind, title, pid, path, pattern, action, session_id, prompt, run_id, signal,
	status, created_at, expires_at, fired_at, delivered, result`

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (Trigger, error) {
	var t Trigger
	var title, path, pattern, sid, prompt, runID, signal, result sql.NullString
	var pid, created, expires, fired, delivered sql.NullInt64
	err := r.Scan(&t.ID, &t.Kind, &title, &pid, &path, &pattern, &t.Action, &sid, &prompt, &runID, &signal,
		&t.Status, &created, &expires, &fired, &delivered, &result)
	if err != nil {
		return t, err
	}
	t.Title, t.Path, t.Pattern = title.String, path.String, pattern.String
	t.SessionID, t.Prompt, t.RunID, t.Signal, t.Result = sid.String, prompt.String, runID.String, signal.String, result.String
	t.PID, t.CreatedAt, t.ExpiresAt, t.FiredAt = int(pid.Int64), created.Int64, expires.Int64, fired.Int64
	t.Delivered = delivered.Int64 != 0
	return t, nil
}

// Create validates and stores a new armed trigger.
func (s *Store) Create(t Trigger) (Trigger, error) {
	if err := t.Validate(); err != nil {
		return Trigger{}, err
	}
	t.ID = "trg_" + uuid.New().String()[:12]
	t.Status = StatusArmed
	t.CreatedAt = time.Now().Unix()
	t.FiredAt, t.Delivered, t.Result = 0, false, ""
	_, err := s.db.Exec(`INSERT INTO triggers (`+cols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.Kind, t.Title, t.PID, t.Path, t.Pattern, t.Action, t.SessionID, t.Prompt, t.RunID, t.Signal,
		t.Status, t.CreatedAt, t.ExpiresAt, 0, 0, "")
	return t, err
}

func (s *Store) Get(id string) (Trigger, error) {
	return scan(s.db.QueryRow(`SELECT `+cols+` FROM triggers WHERE id = ?`, id))
}

func (s *Store) list(where string, args ...any) ([]Trigger, error) {
	rows, err := s.db.Query(`SELECT `+cols+` FROM triggers `+where+` ORDER BY created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Trigger
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) List() ([]Trigger, error)      { return s.list("") }
func (s *Store) ListArmed() ([]Trigger, error) { return s.list(`WHERE status = ?`, StatusArmed) }

// ListUndelivered returns fired triggers whose action never completed (e.g. crash after firing).
func (s *Store) ListUndelivered() ([]Trigger, error) {
	return s.list(`WHERE status = ? AND delivered = 0`, StatusFired)
}

// MarkFired atomically moves armed -> fired. Exactly one caller (across goroutines AND processes
// sharing the DB) gets true, which is what guarantees a trigger wakes the agent only once.
func (s *Store) MarkFired(id, result string) (bool, error) {
	res, err := s.db.Exec(`UPDATE triggers SET status = ?, fired_at = ?, result = ? WHERE id = ? AND status = ?`,
		StatusFired, time.Now().Unix(), result, id, StatusArmed)
	return affected(res, err)
}

func (s *Store) MarkDelivered(id, result string) error {
	_, err := s.db.Exec(`UPDATE triggers SET delivered = 1, result = ? WHERE id = ?`, result, id)
	return err
}

func (s *Store) setResult(id, result string) error {
	_, err := s.db.Exec(`UPDATE triggers SET result = ? WHERE id = ?`, result, id)
	return err
}

// Cancel moves armed -> cancelled.
func (s *Store) Cancel(id string) (bool, error) { return s.setIfArmed(id, StatusCancelled) }

// Expire moves armed -> expired.
func (s *Store) Expire(id string) (bool, error) { return s.setIfArmed(id, StatusExpired) }

func (s *Store) setIfArmed(id string, st Status) (bool, error) {
	res, err := s.db.Exec(`UPDATE triggers SET status = ? WHERE id = ? AND status = ?`, st, id, StatusArmed)
	return affected(res, err)
}

func affected(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return n == 1, nil
}
