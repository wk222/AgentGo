package scheduler

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// LateAfter is how far past its time a job must be fired to count as late (the machine slept or
// the app was closed), as opposed to the normal jitter of the 30 s tick.
const LateAfter = 2 * time.Minute

// Job is a scheduled background task.
type Job struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Spec      string `json:"spec"`
	TZ        string `json:"tz,omitempty"`
	Kind      string `json:"kind"`
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id,omitempty"` // empty: every run opens a new conversation
	Origin    string `json:"origin,omitempty"`     // the conversation whose agent created the job, if one did

	NextRunAt   int64 `json:"next_run_at"`
	IntervalSec int64 `json:"interval_sec"`
	Enabled     bool  `json:"enabled"`
	LastRunAt   int64 `json:"last_run_at,omitempty"`
	FireCount   int   `json:"fire_count"`
	CreatedAt   int64 `json:"created_at"`

	// Set only on the copy Claim hands out: when the run was due, and whether it is being
	// caught up after downtime.
	ScheduledFor int64 `json:"scheduled_for,omitempty"`
	Late         bool  `json:"late,omitempty"`
}

// CreateOptions describes a new job.
type CreateOptions struct {
	Title, Spec, TZ, Prompt, SessionID, Origin string
}

type Store struct {
	db  *sql.DB
	now func() time.Time
}

const jobCols = `id, title, spec, tz, kind, prompt, session_id, origin, next_run_at, interval_sec, enabled, last_run_at, fire_count, created_at`

func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db, now: time.Now}
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS scheduled_jobs (
  id TEXT PRIMARY KEY,
  title TEXT,
  spec TEXT NOT NULL,
  prompt TEXT NOT NULL,
  session_id TEXT,
  next_run_at INTEGER NOT NULL,
  interval_sec INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_run_at INTEGER DEFAULT 0,
  created_at INTEGER NOT NULL
)`); err != nil {
		return nil, err
	}
	// Columns added after the first release; a database from an older version gets them here.
	for _, c := range []struct{ name, ddl string }{
		{"tz", `ALTER TABLE scheduled_jobs ADD COLUMN tz TEXT NOT NULL DEFAULT ''`},
		{"kind", `ALTER TABLE scheduled_jobs ADD COLUMN kind TEXT NOT NULL DEFAULT ''`},
		{"fire_count", `ALTER TABLE scheduled_jobs ADD COLUMN fire_count INTEGER NOT NULL DEFAULT 0`},
		{"origin", `ALTER TABLE scheduled_jobs ADD COLUMN origin TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := s.hasColumn(c.name)
		if err != nil {
			return nil, err
		}
		if !has {
			if _, err := s.db.Exec(c.ddl); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *Store) hasColumn(name string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(scheduled_jobs)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var col, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &col, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if col == name {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Create validates the spec and stores an enabled job.
func (s *Store) Create(title, spec, prompt, sessionID string) (Job, error) {
	return s.CreateWith(CreateOptions{Title: title, Spec: spec, Prompt: prompt, SessionID: sessionID})
}

func (s *Store) CreateWith(o CreateOptions) (Job, error) {
	now := s.now()
	sch, err := ParseSchedule(o.Spec, o.TZ, now)
	if err != nil {
		return Job{}, err
	}
	next, ok := sch.Next(now)
	if !ok {
		return Job{}, fmt.Errorf("这个时间已经过去了，没有下一次执行")
	}
	title := strings.TrimSpace(o.Title)
	if title == "" {
		title = strings.TrimSpace(o.Spec)
	}
	j := Job{
		ID: uuid.NewString(), Title: title, Spec: sch.Canonical(o.Spec), TZ: strings.TrimSpace(o.TZ),
		Kind: string(sch.Kind), Prompt: o.Prompt, SessionID: o.SessionID, Origin: o.Origin,
		NextRunAt: next.Unix(), IntervalSec: intervalOf(sch, now, next), Enabled: true, CreatedAt: now.Unix(),
	}
	_, err = s.db.Exec(`INSERT INTO scheduled_jobs (id, title, spec, tz, kind, prompt, session_id, origin, next_run_at, interval_sec, enabled, fire_count, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,1,0,?)`,
		j.ID, j.Title, j.Spec, j.TZ, j.Kind, j.Prompt, j.SessionID, j.Origin, j.NextRunAt, j.IntervalSec, j.CreatedAt)
	return j, err
}

func intervalOf(sch Schedule, now, next time.Time) int64 {
	if sch.Kind == KindInterval {
		return int64(sch.Every / time.Second)
	}
	return int64(next.Sub(now) / time.Second)
}

func (s *Store) query(where string, args ...any) ([]Job, error) {
	rows, err := s.db.Query(`SELECT `+jobCols+` FROM scheduled_jobs `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanJob(r rowScanner) (Job, error) {
	var j Job
	var en int
	var title, sid sql.NullString
	err := r.Scan(&j.ID, &title, &j.Spec, &j.TZ, &j.Kind, &j.Prompt, &sid, &j.Origin, &j.NextRunAt, &j.IntervalSec, &en, &j.LastRunAt, &j.FireCount, &j.CreatedAt)
	j.Title, j.SessionID, j.Enabled = title.String, sid.String, en == 1
	if j.Kind == "" { // a job made before the column existed
		if sch, perr := ParseSchedule(j.Spec, j.TZ, time.Now()); perr == nil {
			j.Kind = string(sch.Kind)
		}
	}
	return j, err
}

func (s *Store) List() ([]Job, error) {
	return s.query(`ORDER BY next_run_at ASC`)
}

// Due lists enabled jobs whose time has come.
func (s *Store) Due(now int64) ([]Job, error) {
	return s.query(`WHERE enabled=1 AND next_run_at<=? ORDER BY next_run_at ASC`, now)
}

func (s *Store) Get(id string) (Job, error) {
	return scanJob(s.db.QueryRow(`SELECT `+jobCols+` FROM scheduled_jobs WHERE id=?`, id))
}

// advanceTo returns the columns that follow a run at `ran`: the next time, or disabled when the
// schedule is spent or no longer parses (a spec edited by hand must not fire every tick).
func advanceTo(j Job, ran time.Time) (next int64, enabled bool) {
	sch, err := ParseSchedule(j.Spec, j.TZ, ran)
	if err != nil {
		return j.NextRunAt, false
	}
	n, ok := sch.Next(ran)
	if !ok {
		return j.NextRunAt, false
	}
	return n.Unix(), true
}

// Claim takes a due job for firing: it moves the job to its next run first and reports whether
// this caller won, so two processes sharing a database never fire the same run twice and a slow
// run never makes the next tick fire the job again.
func (s *Store) Claim(j Job) (Job, bool, error) {
	now := s.now()
	next, enabled := advanceTo(j, now)
	en := 0
	if enabled {
		en = 1
	}
	res, err := s.db.Exec(`UPDATE scheduled_jobs SET last_run_at=?, next_run_at=?, enabled=?, fire_count=fire_count+1 WHERE id=? AND enabled=1 AND next_run_at=?`,
		now.Unix(), next, en, j.ID, j.NextRunAt)
	if err != nil {
		return j, false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return j, false, nil
	}
	j.ScheduledFor = j.NextRunAt
	j.Late = now.Unix()-j.NextRunAt > int64(LateAfter/time.Second)
	j.LastRunAt, j.NextRunAt, j.Enabled, j.FireCount = now.Unix(), next, enabled, j.FireCount+1
	return j, true, nil
}

// MarkRan records a run the user started by hand. The cadence stays where the schedule puts it.
func (s *Store) MarkRan(id string) error {
	j, err := s.Get(id)
	if err != nil {
		return err
	}
	now := s.now()
	next, enabled := advanceTo(j, now)
	en := 0
	if enabled {
		en = 1
	}
	_, err = s.db.Exec(`UPDATE scheduled_jobs SET last_run_at=?, next_run_at=?, enabled=?, fire_count=fire_count+1 WHERE id=?`,
		now.Unix(), next, en, id)
	return err
}

func (s *Store) SetEnabled(id string, enabled bool) error {
	if !enabled {
		_, err := s.db.Exec(`UPDATE scheduled_jobs SET enabled=0 WHERE id=?`, id)
		return err
	}
	// Re-enabling must not fire a backlog: aim at the next run after now.
	j, err := s.Get(id)
	if err != nil {
		return err
	}
	sch, err := ParseSchedule(j.Spec, j.TZ, s.now())
	if err != nil {
		return err
	}
	next, ok := sch.Next(s.now())
	if !ok {
		return fmt.Errorf("这个任务的时间已经过去了，不能再启用")
	}
	_, err = s.db.Exec(`UPDATE scheduled_jobs SET enabled=1, next_run_at=? WHERE id=?`, next.Unix(), id)
	return err
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_jobs WHERE id=?`, id)
	return err
}
