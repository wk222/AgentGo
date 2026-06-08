package taskhub

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Event types for Wails/SSE-style subscription (task:event).
const (
	EventChunk   = "chunk"
	EventDone    = "done"
	EventError   = "error"
	EventStatus  = "status"
	EventAsk     = "ask"
)

// Task status.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Task is a background job independent of the chat UI thread.
type Task struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // chat, workflow, cron
	SessionID string `json:"session_id,omitempty"`
	Input     string `json:"input,omitempty"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	Result    string `json:"result,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Event is an append-only log entry for reconnect (after_seq).
type Event struct {
	Seq       int64  `json:"seq"`
	TaskID    string `json:"task_id"`
	Type      string `json:"type"`
	Payload   string `json:"payload"`
	CreatedAt int64  `json:"created_at"`
}

// Hub manages background tasks and event streams.
type Hub struct {
	db    *sql.DB
	mu    sync.RWMutex
	emitters []func(taskID string, ev Event)
	runFn    func(ctx context.Context, t Task, emit func(Event)) error
}

func New(db *sql.DB) (*Hub, error) {
	h := &Hub{db: db}
	if err := h.migrate(); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Hub) SetEmitter(fn func(taskID string, ev Event)) {
	h.emitters = []func(taskID string, ev Event){fn}
}

func (h *Hub) AddEmitter(fn func(taskID string, ev Event)) {
	if fn == nil {
		return
	}
	h.emitters = append(h.emitters, fn)
}
func (h *Hub) SetRunner(fn func(ctx context.Context, t Task, emit func(Event)) error) {
	h.runFn = fn
}

func (h *Hub) migrate() error {
	_, err := h.db.Exec(`
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  session_id TEXT,
  input TEXT,
  status TEXT NOT NULL,
  result TEXT,
  error TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS task_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL,
  seq INTEGER NOT NULL,
  type TEXT NOT NULL,
  payload TEXT,
  created_at INTEGER NOT NULL,
  UNIQUE(task_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_task_events_task ON task_events(task_id, seq);
`)
	return err
}

func (h *Hub) appendEvent(taskID, typ, payload string) Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	var seq int64
	_ = h.db.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM task_events WHERE task_id=?`, taskID).Scan(&seq)
	now := time.Now().Unix()
	_, _ = h.db.Exec(`INSERT INTO task_events (task_id, seq, type, payload, created_at) VALUES (?,?,?,?,?)`,
		taskID, seq, typ, payload, now)
	ev := Event{Seq: seq, TaskID: taskID, Type: typ, Payload: payload, CreatedAt: now}
	for _, fn := range h.emitters {
		if fn != nil {
			fn(taskID, ev)
		}
	}
	return ev
}

// Start enqueues and runs a task in the background.
func (h *Hub) Start(kind, sessionID, input string) (Task, error) {
	id := uuid.NewString()
	now := time.Now().Unix()
	t := Task{ID: id, Kind: kind, SessionID: sessionID, Input: input, Status: StatusQueued, CreatedAt: now, UpdatedAt: now}
	_, err := h.db.Exec(`INSERT INTO tasks (id, kind, session_id, input, status, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
		t.ID, t.Kind, t.SessionID, t.Input, t.Status, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return Task{}, err
	}
	h.appendEvent(id, EventStatus, `{"status":"queued"}`)
	go h.runTask(t)
	return t, nil
}

func (h *Hub) runTask(t Task) {
	if h.runFn == nil {
		h.fail(t.ID, "no task runner configured")
		return
	}
	h.setStatus(t.ID, StatusRunning)
	h.appendEvent(t.ID, EventStatus, `{"status":"running"}`)
	ctx := context.Background()
	emit := func(ev Event) {
		if ev.TaskID == "" {
			ev.TaskID = t.ID
		}
		h.appendEvent(t.ID, ev.Type, ev.Payload)
	}
	err := h.runFn(ctx, t, emit)
	if err != nil {
		h.fail(t.ID, err.Error())
		return
	}
	h.complete(t.ID, "")
}

func (h *Hub) setStatus(id, status string) {
	now := time.Now().Unix()
	_, _ = h.db.Exec(`UPDATE tasks SET status=?, updated_at=? WHERE id=?`, status, now, id)
}

func (h *Hub) complete(id, result string) {
	now := time.Now().Unix()
	_, _ = h.db.Exec(`UPDATE tasks SET status=?, result=?, updated_at=? WHERE id=?`, StatusCompleted, result, now, id)
	h.appendEvent(id, EventDone, fmt.Sprintf(`{"result":%q}`, result))
}

func (h *Hub) fail(id, errMsg string) {
	now := time.Now().Unix()
	_, _ = h.db.Exec(`UPDATE tasks SET status=?, error=?, updated_at=? WHERE id=?`, StatusFailed, errMsg, now, id)
	b, _ := json.Marshal(map[string]string{"error": errMsg})
	h.appendEvent(id, EventError, string(b))
}

// List returns recent tasks.
func (h *Hub) List(limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := h.db.Query(`SELECT id, kind, session_id, input, status, result, error, created_at, updated_at FROM tasks ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Kind, &t.SessionID, &t.Input, &t.Status, &t.Result, &t.Error, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EventsSince returns events with seq > afterSeq (reconnect without loss).
func (h *Hub) EventsSince(taskID string, afterSeq int64) ([]Event, error) {
	rows, err := h.db.Query(`SELECT seq, task_id, type, payload, created_at FROM task_events WHERE task_id=? AND seq>? ORDER BY seq ASC`, taskID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Seq, &e.TaskID, &e.Type, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (h *Hub) Get(taskID string) (Task, error) {
	var t Task
	err := h.db.QueryRow(`SELECT id, kind, session_id, input, status, result, error, created_at, updated_at FROM tasks WHERE id=?`, taskID).
		Scan(&t.ID, &t.Kind, &t.SessionID, &t.Input, &t.Status, &t.Result, &t.Error, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}
