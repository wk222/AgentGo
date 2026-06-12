package checkpoint

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSQLiteStore_HistoryTracking(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store, err := OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	cpID := "sess_test_1"

	// 1. Write first version
	p1 := []byte("payload-v1")
	if err := store.Set(ctx, cpID, p1); err != nil {
		t.Fatalf("first set failed: %v", err)
	}

	// 2. Write second version
	p2 := []byte("payload-v2")
	if err := store.Set(ctx, cpID, p2); err != nil {
		t.Fatalf("second set failed: %v", err)
	}

	// 3. Get latest
	latest, ok, err := store.Get(ctx, cpID)
	if err != nil || !ok {
		t.Fatalf("get latest failed: ok=%v, err=%v", ok, err)
	}
	if !bytes.Equal(latest, p2) {
		t.Fatalf("expected payload-v2, got %s", string(latest))
	}

	// 4. List history
	history, err := store.ListHistory(ctx, cpID)
	if err != nil {
		t.Fatalf("list history failed: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(history))
	}
	if history[0].Version != 2 || history[1].Version != 1 {
		t.Fatalf("unexpected versions: v[0]=%d, v[1]=%d", history[0].Version, history[1].Version)
	}

	// 5. Query specific version payload
	v1Payload, ok, err := store.GetVersionPayload(ctx, cpID, 1)
	if err != nil || !ok {
		t.Fatalf("get version 1 payload failed: ok=%v, err=%v", ok, err)
	}
	if !bytes.Equal(v1Payload, p1) {
		t.Fatalf("expected payload-v1, got %s", string(v1Payload))
	}
}
