package admin

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestStore_MultiStepTask(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	task, err := store.CreateTask(ctx, "步骤一；步骤二；步骤三")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Steps) < 2 {
		t.Fatalf("steps: %v", task.Steps)
	}
	_ = store.UpdateStatus(ctx, task.ID, StatusRunning, "")
	n, _ := store.RecoverStuckRunning(ctx)
	if n != 1 {
		t.Fatalf("recover running: %d", n)
	}
	got, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending {
		t.Fatalf("status after recover: %s", got.Status)
	}
}
