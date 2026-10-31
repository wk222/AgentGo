package taskhub

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestHub(t *testing.T, run func(ctx context.Context, tk Task, emit func(Event)) error) *Hub {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// An in-memory database exists per connection; the hub talks to it from several goroutines.
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	h, err := New(conn)
	if err != nil {
		t.Fatal(err)
	}
	h.SetRunner(run)
	return h
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusOf(t *testing.T, h *Hub, id string) string {
	t.Helper()
	tk, err := h.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return tk.Status
}

func TestActiveListsWhatRunsHereAndForgetsWhatEnded(t *testing.T) {
	release := make(chan struct{})
	h := newTestHub(t, func(ctx context.Context, tk Task, emit func(Event)) error {
		<-release
		return nil
	})
	first, err := h.Start("manual", "s1", "第一件事")
	if err != nil {
		t.Fatal(err)
	}
	second, err := h.Start("cron", "s2", "第二件事")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "both tasks to be running", func() bool {
		a := h.Active()
		return len(a) == 2 && a[0].Status == StatusRunning && a[1].Status == StatusRunning
	})
	a := h.Active()
	got := map[string]Task{a[0].ID: a[0], a[1].ID: a[1]}
	if got[first.ID].Kind != "manual" || got[first.ID].SessionID != "s1" || got[first.ID].Input != "第一件事" {
		t.Fatalf("first = %+v", got[first.ID])
	}
	if got[second.ID].Kind != "cron" || got[second.ID].CreatedAt == 0 {
		t.Fatalf("second = %+v", got[second.ID])
	}

	close(release)
	waitFor(t, "the tasks to end", func() bool { return len(h.Active()) == 0 })
	waitFor(t, "completed rows", func() bool {
		return statusOf(t, h, first.ID) == StatusCompleted && statusOf(t, h, second.ID) == StatusCompleted
	})
}

// A row a dead process left at "running" is not running here: Active only knows
// what this process started.
func TestActiveIgnoresRowsThisProcessDidNotStart(t *testing.T) {
	h := newTestHub(t, func(ctx context.Context, tk Task, emit func(Event)) error { return nil })
	if _, err := h.db.Exec(`INSERT INTO tasks (id, kind, session_id, input, status, created_at, updated_at) VALUES ('ghost','manual','s','x','running',1,1)`); err != nil {
		t.Fatal(err)
	}
	if a := h.Active(); len(a) != 0 {
		t.Fatalf("active = %+v", a)
	}
	if h.Cancel("ghost") {
		t.Fatal("cancelled a task this process never started")
	}
}

func TestCancelStopsARunningTask(t *testing.T) {
	started := make(chan struct{})
	h := newTestHub(t, func(ctx context.Context, tk Task, emit func(Event)) error {
		close(started)
		<-ctx.Done()
		// Like the real runner: it reports the interruption as an error before returning it.
		emit(Event{Type: EventError, Payload: `{"error":"context canceled"}`})
		return ctx.Err()
	})
	tk, err := h.Start("manual", "", "长任务")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if !h.Cancel(tk.ID) {
		t.Fatal("Cancel refused a running task")
	}
	waitFor(t, "the task to end", func() bool { return len(h.Active()) == 0 })
	waitFor(t, "a cancelled row", func() bool { return statusOf(t, h, tk.ID) == StatusCancelled })

	events, err := h.EventsSince(tk.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Type != EventStatus || !strings.Contains(last.Payload, "cancelled") {
		t.Fatalf("last event = %+v", last)
	}
	for _, ev := range events {
		if ev.Type == EventError {
			t.Fatalf("stopping the task was logged as an error: %+v", ev)
		}
	}
	if h.Cancel(tk.ID) {
		t.Fatal("Cancel claimed to stop a task that is already over")
	}
}

func TestACancelledTaskThatFinishedAnywayKeepsItsResult(t *testing.T) {
	started := make(chan struct{})
	h := newTestHub(t, func(ctx context.Context, tk Task, emit func(Event)) error {
		close(started)
		<-ctx.Done()
		return nil // wrapped up cleanly despite the request
	})
	tk, err := h.Start("manual", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	h.Cancel(tk.ID)
	waitFor(t, "a finished row", func() bool { return statusOf(t, h, tk.ID) == StatusCompleted })
}

// A task is stored without a result or an error, and a finished one has only one
// of the two, so every row has a NULL column. Listing used to fail on the first
// task, which left the task list empty whenever there was anything to show.
func TestListAndGetReadTasksInEveryState(t *testing.T) {
	release := make(chan struct{})
	h := newTestHub(t, func(ctx context.Context, tk Task, emit func(Event)) error {
		switch tk.Input {
		case "bad":
			return errors.New("boom")
		case "wait":
			<-release
		}
		return nil
	})
	defer close(release)
	ok, err := h.Start("manual", "s1", "ok")
	if err != nil {
		t.Fatal(err)
	}
	bad, err := h.Start("manual", "", "bad")
	if err != nil {
		t.Fatal(err)
	}
	wait, err := h.Start("cron", "", "wait")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first two to finish", func() bool {
		return statusOf(t, h, ok.ID) == StatusCompleted && statusOf(t, h, bad.ID) == StatusFailed
	})
	waitFor(t, "the third to run", func() bool { return statusOf(t, h, wait.ID) == StatusRunning })

	list, err := h.List(10)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Task{}
	for _, tk := range list {
		got[tk.ID] = tk
	}
	if len(got) != 3 {
		t.Fatalf("listed %d tasks, want 3: %+v", len(got), list)
	}
	if got[ok.ID].Status != StatusCompleted || got[ok.ID].SessionID != "s1" || got[ok.ID].Error != "" {
		t.Fatalf("completed = %+v", got[ok.ID])
	}
	if got[bad.ID].Status != StatusFailed || got[bad.ID].Error != "boom" || got[bad.ID].Result != "" {
		t.Fatalf("failed = %+v", got[bad.ID])
	}
	if got[wait.ID].Status != StatusRunning || got[wait.ID].Kind != "cron" {
		t.Fatalf("running = %+v", got[wait.ID])
	}
}

func TestAFailingTaskIsFailedNotCancelled(t *testing.T) {
	h := newTestHub(t, func(ctx context.Context, tk Task, emit func(Event)) error {
		return errors.New("boom")
	})
	tk, err := h.Start("manual", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a failed row", func() bool { return statusOf(t, h, tk.ID) == StatusFailed })
	if len(h.Active()) != 0 {
		t.Fatalf("active = %+v", h.Active())
	}
}
