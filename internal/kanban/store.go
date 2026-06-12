package kanban

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Board struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type Task struct {
	ID      string `json:"id"`
	BoardID string `json:"board_id"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Sort    int    `json:"sort"`
}

type Store struct {
	db *sql.DB
}

func Open(db *sql.DB) (*Store, error) {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS kanban_boards (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, created_at INTEGER
		);
		CREATE TABLE IF NOT EXISTS kanban_tasks (
			id TEXT PRIMARY KEY, board_id TEXT NOT NULL, title TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'todo', sort_order INTEGER DEFAULT 0,
			created_at INTEGER, updated_at INTEGER
		);
	`)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	_ = s.seedDefault(context.Background())
	return s, nil
}

func (s *Store) seedDefault(ctx context.Context) error {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kanban_boards`).Scan(&n)
	if n > 0 {
		return nil
	}
	b := Board{ID: "default", Title: "AgentGo 计划投影"}
	_ = s.CreateBoard(ctx, b)
	tasks := []Task{
		{Title: "整理 Memory 工作台", Status: "doing"},
		{Title: "复用 Eino checkpoint Resume", Status: "done"},
		{Title: "把计划看板作为任务投影视图", Status: "todo"},
	}
	for i, t := range tasks {
		t.BoardID = b.ID
		t.Sort = i
		_ = s.CreateTask(ctx, t)
	}
	return nil
}

func (s *Store) ListBoards(ctx context.Context) ([]Board, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title FROM kanban_boards ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Board
	for rows.Next() {
		var b Board
		if err := rows.Scan(&b.ID, &b.Title); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

func (s *Store) ListTasks(ctx context.Context, boardID string) ([]Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, board_id, title, status, sort_order FROM kanban_tasks
		WHERE board_id = ? ORDER BY sort_order, created_at
	`, boardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.BoardID, &t.Title, &t.Status, &t.Sort); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) CreateBoard(ctx context.Context, b Board) error {
	if b.ID == "" {
		b.ID = "board_" + uuid.New().String()[:8]
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO kanban_boards (id, title, created_at) VALUES (?, ?, ?)`,
		b.ID, b.Title, time.Now().Unix())
	return err
}

func (s *Store) CreateTask(ctx context.Context, t Task) error {
	if t.ID == "" {
		t.ID = "task_" + uuid.New().String()[:8]
	}
	if t.Status == "" {
		t.Status = "todo"
	}
	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO kanban_tasks (id, board_id, title, status, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, t.ID, t.BoardID, t.Title, t.Status, t.Sort, now, now)
	return err
}

func (s *Store) UpdateTaskStatus(ctx context.Context, taskID, status string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE kanban_tasks SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().Unix(), taskID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found")
	}
	return nil
}

func (s *Store) TaskCount(ctx context.Context, boardID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM kanban_tasks WHERE board_id = ?`, boardID).Scan(&n)
	return n, err
}
