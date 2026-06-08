package sessions

import (
	"context"
	"testing"

	agentdb "agentgo/internal/db"

	_ "modernc.org/sqlite"
	"database/sql"
)

func TestGetMessagesIsolatedBySessionID(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := agentdb.Configure(conn); err != nil {
		t.Fatal(err)
	}
	store, err := Open(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := store.Create(ctx, "A")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Create(ctx, "B")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(ctx, a.ID, "user", "only in A", "text", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendMessage(ctx, b.ID, "user", "only in B", "text", nil); err != nil {
		t.Fatal(err)
	}
	msgsA, err := store.GetMessages(ctx, a.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgsA) != 1 || msgsA[0].Content != "only in A" {
		t.Fatalf("session A: got %+v", msgsA)
	}
	msgsB, err := store.GetMessages(ctx, b.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgsB) != 1 || msgsB[0].Content != "only in B" {
		t.Fatalf("session B: got %+v", msgsB)
	}
}

func TestAutoTitleFromUserMessage(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := agentdb.Configure(conn); err != nil {
		t.Fatal(err)
	}
	store, err := Open(conn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sess, err := store.Create(ctx, "新对话")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AutoTitleFromUserMessage(ctx, sess.ID, "nihao"); err != nil {
		t.Fatal(err)
	}
	list, err := store.List(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "nihao" {
		t.Fatalf("title: %+v", list)
	}
}
