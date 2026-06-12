package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type TaskStatus string

const (
	StatusPending   TaskStatus = "pending"
	StatusRunning   TaskStatus = "running"
	StatusPaused    TaskStatus = "paused"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
)

// DurableTask is a multi-step admin goal persisted across sessions.
type DurableTask struct {
	ID          string     `json:"id"`
	Goal        string     `json:"goal"`
	Status      TaskStatus `json:"status"`
	Steps       []string   `json:"steps"`
	CurrentStep int        `json:"current_step"`
	LastError   string     `json:"last_error,omitempty"`
	CreatedAt   int64      `json:"created_at"`
	UpdatedAt   int64      `json:"updated_at"`
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS admin_durable_tasks (
			id TEXT PRIMARY KEY,
			goal TEXT NOT NULL,
			status TEXT NOT NULL,
			steps_json TEXT DEFAULT '[]',
			current_step INTEGER DEFAULT 0,
			last_error TEXT,
			created_at INTEGER,
			updated_at INTEGER
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
	`)
	if err != nil {
		return nil, err
	}
	_, _ = s.db.Exec(`ALTER TABLE admin_durable_tasks ADD COLUMN steps_json TEXT DEFAULT '[]'`)
	_, _ = s.db.Exec(`ALTER TABLE admin_durable_tasks ADD COLUMN current_step INTEGER DEFAULT 0`)
	return s, nil
}

func (s *Store) CreateTask(ctx context.Context, goal string) (*DurableTask, error) {
	steps := DefaultPlanSteps(goal)
	return s.CreateTaskWithSteps(ctx, goal, steps)
}

func (s *Store) CreateTaskWithSteps(ctx context.Context, goal string, steps []string) (*DurableTask, error) {
	now := time.Now().Unix()
	stepsJSON, _ := json.Marshal(steps)
	task := &DurableTask{
		ID:          fmt.Sprintf("task_%s", uuid.New().String()[:12]),
		Goal:        goal,
		Status:      StatusPending,
		Steps:       steps,
		CurrentStep: 0,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO admin_durable_tasks (id, goal, status, steps_json, current_step, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, task.ID, task.Goal, string(task.Status), string(stepsJSON), task.CurrentStep, task.CreatedAt, task.UpdatedAt)
	return task, err
}

func (s *Store) UpdateStatus(ctx context.Context, id string, status TaskStatus, lastError string) error {
	task, err := s.GetTask(ctx, id)
	if err != nil {
		return err
	}
	if err := AssertTransition(task.Status, status); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE admin_durable_tasks SET status = ?, last_error = ?, updated_at = ? WHERE id = ?
	`, string(status), lastError, time.Now().Unix(), id)
	return err
}

func (s *Store) AdvanceStep(ctx context.Context, id string, nextStep int, status TaskStatus) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE admin_durable_tasks SET current_step = ?, status = ?, updated_at = ? WHERE id = ?
	`, nextStep, string(status), time.Now().Unix(), id)
	return err
}

// ReplanSteps replaces steps and resets progress (PyBot admin replan).
func (s *Store) ReplanSteps(ctx context.Context, id string, steps []string, status TaskStatus) error {
	stepsJSON, _ := json.Marshal(steps)
	_, err := s.db.ExecContext(ctx, `
		UPDATE admin_durable_tasks SET steps_json = ?, current_step = 0, status = ?, last_error = '', updated_at = ? WHERE id = ?
	`, string(stepsJSON), string(status), time.Now().Unix(), id)
	return err
}

func (s *Store) RecoverStuckRunning(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE admin_durable_tasks SET status = ?, updated_at = ? WHERE status = ?
	`, string(StatusPending), time.Now().Unix(), string(StatusRunning))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) GetPendingOrRunningTasks(ctx context.Context) ([]DurableTask, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, goal, status, steps_json, current_step, last_error, created_at, updated_at
		FROM admin_durable_tasks
		WHERE status IN ('pending', 'running')
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []DurableTask
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *Store) ListAllTasks(ctx context.Context, limit, offset int) ([]DurableTask, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, goal, status, steps_json, current_step, last_error, created_at, updated_at
		FROM admin_durable_tasks
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []DurableTask
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, nil
}

func (s *Store) GetTask(ctx context.Context, id string) (*DurableTask, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, goal, status, steps_json, current_step, last_error, created_at, updated_at
		FROM admin_durable_tasks WHERE id = ?
	`, id)
	t, err := scanTaskRow(row)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTask(rows *sql.Rows) (DurableTask, error) {
	return scanTaskRow(rows)
}

func scanTaskRow(row rowScanner) (DurableTask, error) {
	var t DurableTask
	var stepsStr string
	var lastErr sql.NullString
	if err := row.Scan(&t.ID, &t.Goal, &t.Status, &stepsStr, &t.CurrentStep, &lastErr, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return t, err
	}
	if lastErr.Valid {
		t.LastError = lastErr.String
	}
	_ = json.Unmarshal([]byte(stepsStr), &t.Steps)
	return t, nil
}

// StepTrace tracks execution details for a single step in a durable task.
type StepTrace struct {
	TaskID     string `json:"task_id"`
	StepIndex  int    `json:"step_index"`
	Action     string `json:"action"`
	Input      string `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

func (s *Store) SaveStepTrace(ctx context.Context, t StepTrace) error {
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().Unix()
	}
	var existingID string
	err := s.db.QueryRowContext(ctx, `
		SELECT id FROM governance_audit_log 
		WHERE session_id = ? AND step_index = ? AND channel = 'admin' LIMIT 1
	`, t.TaskID, t.StepIndex).Scan(&existingID)
	if err == sql.ErrNoRows {
		id := uuid.New().String()
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO governance_audit_log (
				id, timestamp, channel, session_id, action, arguments, result, explanation, step_index, duration_ms
			) VALUES (?, ?, 'admin', ?, ?, ?, ?, ?, ?, ?)
		`, id, t.CreatedAt, t.TaskID, t.Action, t.Input, t.Output, t.Error, t.StepIndex, t.DurationMS)
		return err
	} else if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE governance_audit_log SET
			timestamp = ?, action = ?, arguments = ?, result = ?, explanation = ?, duration_ms = ?
		WHERE id = ?
	`, t.CreatedAt, t.Action, t.Input, t.Output, t.Error, t.DurationMS, existingID)
	return err
}

func (s *Store) ListStepTraces(ctx context.Context, taskID string) ([]StepTrace, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, step_index, action, arguments, result, duration_ms, explanation, timestamp
		FROM governance_audit_log WHERE session_id = ? AND channel = 'admin' ORDER BY step_index ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StepTrace
	for rows.Next() {
		var t StepTrace
		var inputVal, outputVal, errorVal sql.NullString
		var stepIndexVal, durationVal sql.NullInt64
		err := rows.Scan(&t.TaskID, &stepIndexVal, &t.Action, &inputVal, &outputVal, &durationVal, &errorVal, &t.CreatedAt)
		if err != nil {
			return nil, err
		}
		if stepIndexVal.Valid {
			t.StepIndex = int(stepIndexVal.Int64)
		}
		if durationVal.Valid {
			t.DurationMS = durationVal.Int64
		}
		if inputVal.Valid {
			t.Input = inputVal.String
		}
		if outputVal.Valid {
			t.Output = outputVal.String
		}
		if errorVal.Valid {
			t.Error = errorVal.String
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
