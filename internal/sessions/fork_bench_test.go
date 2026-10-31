package sessions

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bigStore is a file-backed store opened the way production opens agentgo.db
// (one connection, WAL), holding one long conversation: msgs transcript rows of
// about 800 bytes and as many context events of about 2 KiB, in one scope.
func bigStore(tb testing.TB, msgs int) *Store {
	tb.Helper()
	db, err := sql.Open("sqlite", filepath.Join(tb.TempDir(), "big.db"))
	if err != nil {
		tb.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	tb.Cleanup(func() { db.Close() })
	for _, p := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=10000", "PRAGMA synchronous=NORMAL", "PRAGMA temp_store=MEMORY", "PRAGMA foreign_keys=ON"} {
		if _, err = db.Exec(p); err != nil {
			tb.Fatal(err)
		}
	}
	s, err := Open(db)
	if err != nil {
		tb.Fatal(err)
	}
	ctx := context.Background()
	if err = s.EnsureSession(ctx, "big"); err != nil {
		tb.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		tb.Fatal(err)
	}
	text := strings.Repeat("长对话里的一段话。", 30) // about 800 bytes of UTF-8
	payload := []byte(strings.Repeat("e", 2048))
	now := time.Now().Unix()
	for i := 0; i < msgs; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO chat_messages (id, session_id, role, content, msg_type, meta_json, created_at) VALUES (?, 'big', ?, ?, 'text', '', ?)`,
			fmt.Sprintf("msg_big_%d", i), role, text, now+int64(i/50)); err != nil {
			tb.Fatal(err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO chat_context_events(session_id, scope, seq, event_id, kind, payload) VALUES ('big', 'eino-v1:bench', ?, ?, 'message', ?)`,
			i+1, fmt.Sprintf("ev_%d", i), payload); err != nil {
			tb.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		tb.Fatal(err)
	}
	return s
}

// BenchmarkForkSession times a fork of a conversation of about 55 MB (20 000
// messages and 20 000 context events): the hand-off the user waits for when
// they click "fork" on a very long conversation. Run it with
//
//	go test ./internal/sessions -run xxx -bench ForkSession -benchtime 3x
func BenchmarkForkSession(b *testing.B) {
	s := bigStore(b, 20000)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := s.ForkSession(ctx, "big", "")
		if err != nil {
			b.Fatal(err)
		}
		if res.Messages != 20000 || res.Events != 20000 {
			b.Fatalf("copied %d messages and %d events", res.Messages, res.Events)
		}
		b.StopTimer()
		if err = s.Delete(ctx, res.Session.ID); err != nil { // keep the file from growing between rounds
			b.Fatal(err)
		}
		b.StartTimer()
	}
}
