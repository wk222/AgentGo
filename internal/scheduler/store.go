package scheduler

import (
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Job is a scheduled background task (natural language or cron-like spec).
type Job struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Spec        string `json:"spec"`
	Prompt      string `json:"prompt"`
	SessionID   string `json:"session_id,omitempty"`
	NextRunAt   int64  `json:"next_run_at"`
	IntervalSec int64  `json:"interval_sec"`
	Enabled     bool   `json:"enabled"`
	LastRunAt   int64  `json:"last_run_at,omitempty"`
	CreatedAt   int64  `json:"created_at"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	_, err := s.db.Exec(`
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
)`)
	return s, err
}

// ParseNaturalSpec converts simple Chinese/English phrases to interval seconds.
// Examples: "每30分钟", "every 1h", "每天9点" (approx: 24h).
func ParseNaturalSpec(spec string) (intervalSec int64, title string, err error) {
	spec = strings.TrimSpace(strings.ToLower(spec))
	title = spec
	reEvery := regexp.MustCompile(`(?:每|every)\s*(\d+)\s*(分钟|分|min|m|小时|h|hour|天|d|day)`)
	if m := reEvery.FindStringSubmatch(spec); len(m) == 3 {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		unit := m[2]
		switch {
		case strings.HasPrefix(unit, "分"), unit == "min", unit == "m":
			return n * 60, title, nil
		case strings.HasPrefix(unit, "小时"), unit == "h", unit == "hour":
			return n * 3600, title, nil
		default:
			return n * 86400, title, nil
		}
	}
	if strings.Contains(spec, "每天") || strings.Contains(spec, "daily") {
		return 86400, title, nil
	}
	return 0, "", fmt.Errorf("无法解析定时描述，请使用如「每30分钟」或「every 1h」")
}

func (s *Store) Create(title, spec, prompt, sessionID string) (Job, error) {
	interval, t, err := ParseScheduleSpec(spec)
	if err != nil {
		return Job{}, err
	}
	if title == "" {
		title = t
	}
	now := time.Now().Unix()
	j := Job{
		ID: uuid.NewString(), Title: title, Spec: spec, Prompt: prompt, SessionID: sessionID,
		NextRunAt: now + interval, IntervalSec: interval, Enabled: true, CreatedAt: now,
	}
	_, err = s.db.Exec(`INSERT INTO scheduled_jobs (id, title, spec, prompt, session_id, next_run_at, interval_sec, enabled, created_at) VALUES (?,?,?,?,?,?,?,1,?)`,
		j.ID, j.Title, j.Spec, j.Prompt, j.SessionID, j.NextRunAt, j.IntervalSec, j.CreatedAt)
	return j, err
}

func (s *Store) List() ([]Job, error) {
	rows, err := s.db.Query(`SELECT id, title, spec, prompt, session_id, next_run_at, interval_sec, enabled, last_run_at, created_at FROM scheduled_jobs ORDER BY next_run_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var en int
		if err := rows.Scan(&j.ID, &j.Title, &j.Spec, &j.Prompt, &j.SessionID, &j.NextRunAt, &j.IntervalSec, &en, &j.LastRunAt, &j.CreatedAt); err != nil {
			return nil, err
		}
		j.Enabled = en == 1
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) Due(now int64) ([]Job, error) {
	rows, err := s.db.Query(`SELECT id, title, spec, prompt, session_id, next_run_at, interval_sec, enabled, last_run_at, created_at FROM scheduled_jobs WHERE enabled=1 AND next_run_at<=?`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var en int
		if err := rows.Scan(&j.ID, &j.Title, &j.Spec, &j.Prompt, &j.SessionID, &j.NextRunAt, &j.IntervalSec, &en, &j.LastRunAt, &j.CreatedAt); err != nil {
			return nil, err
		}
		j.Enabled = en == 1
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) BumpNext(id string, intervalSec int64) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`UPDATE scheduled_jobs SET last_run_at=?, next_run_at=? WHERE id=?`, now, now+intervalSec, id)
	return err
}

func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM scheduled_jobs WHERE id=?`, id)
	return err
}
