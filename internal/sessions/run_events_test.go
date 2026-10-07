package sessions

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	agentdb "agentgo/internal/db"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := agentdb.Configure(conn); err != nil {
		t.Fatal(err)
	}
	st, err := Open(conn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return st
}

func TestRunEventsAreNumberedPerSessionAndResumable(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		seq, err := st.AppendRunEvent(ctx, RunEvent{SessionID: "a", RunID: "r1", Kind: KindToolCall, CallID: fmt.Sprint(i), Name: "t"})
		if err != nil || seq != int64(i) {
			t.Fatalf("a: seq=%d err=%v, want %d", seq, err, i)
		}
	}
	if seq, err := st.AppendRunEvent(ctx, RunEvent{SessionID: "b", RunID: "r9", Kind: KindStatus}); err != nil || seq != 1 {
		t.Fatalf("numbering must be per session: seq=%d err=%v", seq, err)
	}

	// A client that last saw seq 1 gets exactly the rest, in order.
	got, err := st.ListRunEvents(ctx, "a", 1, 0)
	if err != nil || len(got) != 2 || got[0].Seq != 2 || got[1].Seq != 3 || got[0].CallID != "2" {
		t.Fatalf("after=1 -> %+v err=%v", got, err)
	}
	if got, _ := st.ListRunEvents(ctx, "a", 3, 0); len(got) != 0 {
		t.Fatalf("nothing is newer than the last seq, got %+v", got)
	}
	if got, _ := st.ListRunEvents(ctx, "a", 0, 2); len(got) != 2 {
		t.Fatalf("limit not applied: %d", len(got))
	}
}

func TestRunEventsConcurrentAppendsNeverCollide(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	const n = 40
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.AppendRunEvent(ctx, RunEvent{SessionID: "s", RunID: "r", Kind: KindToolResult})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.ListRunEvents(ctx, "s", 0, 0)
	if err != nil || len(got) != n {
		t.Fatalf("stored %d of %d, err=%v", len(got), n, err)
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("gap or duplicate at index %d: seq=%d", i, e.Seq)
		}
	}
}

func TestLastRunStateIsDerivedFromTheLog(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	add := func(run, kind, payload string) {
		t.Helper()
		if _, err := st.AppendRunEvent(ctx, RunEvent{SessionID: "s", RunID: run, Kind: kind, PayloadJSON: payload}); err != nil {
			t.Fatal(err)
		}
	}
	state := func() RunState {
		t.Helper()
		rs, err := st.LastRunState(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}

	if rs := state(); rs.Status != RunNone {
		t.Fatalf("empty log = %+v", rs)
	}
	add("r1", KindStatus, `{"state":"started"}`)
	add("r1", KindToolCall, `{}`)
	if rs := state(); rs.Status != RunUnfinished || rs.RunID != "r1" {
		t.Fatalf("no commit point yet = %+v (a crash would leave exactly this)", rs)
	}
	add("r1", KindDone, `{"error":"","pending":false}`)
	if rs := state(); rs.Status != RunCompleted {
		t.Fatalf("done = %+v", rs)
	}

	add("r2", KindStatus, `{}`)
	add("r2", KindDone, `{"pending":true}`)
	if rs := state(); rs.Status != RunAwaitingApproval || rs.RunID != "r2" {
		t.Fatalf("paused = %+v", rs)
	}

	add("r3", KindStatus, `{}`)
	add("r3", KindDone, `{"error":"model unreachable"}`)
	if rs := state(); rs.Status != RunFailed || rs.Error != "model unreachable" {
		t.Fatalf("failed = %+v", rs)
	}
	if rs := state(); rs.Seq != 7 {
		t.Fatalf("seq = %d, want 7", rs.Seq)
	}
}

// A run that paused commits once as pending, then carries on when resumed. If
// the process dies after it carried on but before it committed again, the log
// must not claim the run is still waiting for approval.
func TestResumedRunAfterCrashIsUnfinishedNotAwaiting(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	add := func(kind, payload string) {
		t.Helper()
		if _, err := st.AppendRunEvent(ctx, RunEvent{SessionID: "s", RunID: "r", Kind: kind, PayloadJSON: payload}); err != nil {
			t.Fatal(err)
		}
	}
	status := func() RunStatus {
		t.Helper()
		rs, err := st.LastRunState(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		return rs.Status
	}
	add(KindStatus, `{}`)
	add(KindDone, `{"pending":true}`)
	if got := status(); got != RunAwaitingApproval {
		t.Fatalf("paused = %s", got)
	}
	add(KindStatus, `{"state":"resumed"}`)
	if got := status(); got != RunUnfinished {
		t.Fatalf("resumed but not yet committed = %s, want %s", got, RunUnfinished)
	}
	add(KindDone, `{"pending":false}`)
	if got := status(); got != RunCompleted {
		t.Fatalf("finished after resume = %s", got)
	}
}

func TestDeleteSessionRemovesItsRunEvents(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	s, err := st.Create(ctx, "x")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendRunEvent(ctx, RunEvent{SessionID: s.ID, RunID: "r", Kind: KindStatus}); err != nil {
		t.Fatal(err)
	}
	if err := st.Delete(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ListRunEvents(ctx, s.ID, 0, 0); len(got) != 0 {
		t.Fatalf("events survived the session: %+v", got)
	}
}

// Long sessions used to return the OLDEST `limit` rows, so the model history
// stopped growing once a session passed the limit.
func TestGetMessagesReturnsTheNewestInOrder(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	s, err := st.Create(ctx, "long")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ { // all within one second: only insertion order can separate them
		if err := st.AppendMessage(ctx, s.ID, "user", fmt.Sprintf("m%02d", i), "text", nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.GetMessages(ctx, s.ID, 4)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"m07", "m08", "m09", "m10"}
	if len(got) != len(want) {
		t.Fatalf("got %d messages", len(got))
	}
	for i := range want {
		if got[i].Content != want[i] {
			t.Fatalf("message %d = %q, want %q (all: %+v)", i, got[i].Content, want[i], got)
		}
	}
}
