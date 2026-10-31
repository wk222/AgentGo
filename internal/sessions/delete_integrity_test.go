package sessions

import (
	"context"
	"database/sql"
	"testing"
)

func deletionStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}
func seedDeletionSession(t *testing.T, s *Store, id string) {
	t.Helper()
	ctx := context.Background()
	if err := s.EnsureSession(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(ctx, id, "user", "keep or delete together", "text", nil); err != nil {
		t.Fatal(err)
	}
	// chat_entries/chat_lanes are legacy tables nobody writes any more. Seed them by hand so the
	// test still proves old rows cannot block deleting a session.
	if _, err := s.db.Exec(`INSERT INTO chat_entries (id, session_id, kind, role, content, created_at) VALUES (?, ?, 'message', 'user', 'branch', 1)`, "entry-"+id, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO chat_lanes (session_id, name, head_entry_id, created_at, updated_at) VALUES (?, 'main', ?, 1, 1)`, id, "entry-"+id); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveMode(ctx, id, ModeRecord{Profile: "coding"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendContextEvents(ctx, id, "scope", []ContextEventRecord{{ID: "native", Kind: "message", Payload: []byte("event")}}, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePendingRun(ctx, PendingRun{ApprovalID: id + "-approval", SessionID: id, OwnerPID: 1, PayloadJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO chat_session_forks (session_id, parent_id, parent_title, forked_at) VALUES (?, 'origin', 'origin title', 1)`, id); err != nil {
		t.Fatal(err)
	}
}
func TestSessionDeleteIsAtomicAndRemovesTree(t *testing.T) {
	for _, bulk := range []bool{false, true} {
		t.Run(map[bool]string{false: "one", true: "batch"}[bulk], func(t *testing.T) {
			s, db := deletionStore(t)
			seedDeletionSession(t, s, "a")
			seedDeletionSession(t, s, "b")
			ctx := context.Background()
			if _, err := db.Exec("CREATE TRIGGER reject_delete BEFORE DELETE ON chat_context_events BEGIN SELECT RAISE(ABORT,'injected storage failure'); END"); err != nil {
				t.Fatal(err)
			}
			var err error
			if bulk {
				_, err = s.DeleteBatch(ctx, []string{"a", "b"})
			} else {
				err = s.Delete(ctx, " a ")
			}
			if err == nil {
				t.Fatal("failed delete reported success")
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM chat_messages").Scan(&count); err != nil || count != 2 {
				t.Fatal("partial transaction", count, err)
			}
			if _, err := db.Exec("DROP TRIGGER reject_delete"); err != nil {
				t.Fatal(err)
			}
			if bulk {
				_, err = s.DeleteBatch(ctx, []string{" a ", "a", "b"})
			} else {
				err = s.Delete(ctx, " a ")
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"chat_messages", "chat_entries", "chat_lanes", "chat_context_events", "chat_session_forks", "pending_runs", "session_modes", "chat_sessions"} {
				col := "session_id"
				if table == "chat_sessions" {
					col = "id"
				}
				if err := db.QueryRow("SELECT COUNT(*) FROM " + table + " WHERE " + col + "='a'").Scan(&count); err != nil || count != 0 {
					t.Fatalf("%s count=%d err=%v", table, count, err)
				}
			}
		})
	}
}
func TestDeleteAllPreservesDefaultAndKeptSession(t *testing.T) {
	s, db := deletionStore(t)
	ctx := context.Background()
	seedDeletionSession(t, s, "keep")
	seedDeletionSession(t, s, "remove")
	if err := s.SaveMode(ctx, "", ModeRecord{Profile: "default"}); err != nil {
		t.Fatal(err)
	}
	n, err := s.DeleteAllExcept(ctx, " keep ")
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	def, ok, modes, err := s.LoadModes(ctx)
	if err != nil || !ok || def.Profile != "default" || len(modes) != 1 {
		t.Fatalf("mode cleanup: %+v %v %+v %v", def, ok, modes, err)
	}
	n, err = s.DeleteAllExcept(ctx, "")
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	def, ok, modes, err = s.LoadModes(ctx)
	if err != nil || !ok || def.Profile != "default" || len(modes) != 0 {
		t.Fatal("lost default configuration", err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM chat_context_events").Scan(&count); err != nil || count != 0 {
		t.Fatal("retained native history", count, err)
	}
}
