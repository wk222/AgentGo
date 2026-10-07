package sessions

import (
	"context"
	"testing"
)

func TestPendingRunsSaveReplaceListDelete(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	if err := st.SavePendingRun(ctx, PendingRun{ApprovalID: "a1", SessionID: "s1", OwnerPID: 10, PayloadJSON: `{"v":1}`}); err != nil {
		t.Fatal(err)
	}
	if err := st.SavePendingRun(ctx, PendingRun{ApprovalID: "a2", SessionID: "s2", OwnerPID: 10, PayloadJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	// Saving again replaces the payload and the owner (adoption), not the row count.
	if err := st.SavePendingRun(ctx, PendingRun{ApprovalID: "a1", SessionID: "s1", OwnerPID: 20, PayloadJSON: `{"v":2}`}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ListPendingRuns(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("list = %+v err=%v", got, err)
	}
	var a1 PendingRun
	for _, p := range got {
		if p.ApprovalID == "a1" {
			a1 = p
		}
	}
	if a1.OwnerPID != 20 || a1.PayloadJSON != `{"v":2}` {
		t.Fatalf("replace lost: %+v", a1)
	}

	if err := st.DeletePendingRun(ctx, "a1"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeletePendingRun(ctx, "missing"); err != nil {
		t.Fatalf("deleting a missing row must not fail: %v", err)
	}
	if got, _ := st.ListPendingRuns(ctx); len(got) != 1 || got[0].ApprovalID != "a2" {
		t.Fatalf("after delete = %+v", got)
	}
	if err := st.SavePendingRun(ctx, PendingRun{}); err == nil {
		t.Fatal("a pending run needs an approval id")
	}
}

func TestDeleteSessionRemovesItsPendingRuns(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	sess, err := st.Create(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SavePendingRun(ctx, PendingRun{ApprovalID: "a1", SessionID: sess.ID, OwnerPID: 1, PayloadJSON: `{}`})
	_ = st.SavePendingRun(ctx, PendingRun{ApprovalID: "a2", SessionID: "other", OwnerPID: 1, PayloadJSON: `{}`})
	if err := st.Delete(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := st.ListPendingRuns(ctx)
	if len(got) != 1 || got[0].ApprovalID != "a2" {
		t.Fatalf("pending runs after session delete = %+v", got)
	}
}

func TestListAwaitingRunsOnlyReturnsLatestPausedCommit(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	add := func(sid, run, kind, payload string) {
		t.Helper()
		if _, err := st.AppendRunEvent(ctx, RunEvent{SessionID: sid, RunID: run, Kind: kind, PayloadJSON: payload}); err != nil {
			t.Fatal(err)
		}
	}
	// paused: latest event is a pending commit
	add("paused", "r1", KindStatus, `{}`)
	add("paused", "r1", KindDone, `{"pending":true}`)
	// completed: paused once, then resumed and finished
	add("completed", "r2", KindDone, `{"pending":true}`)
	add("completed", "r2", KindStatus, `{"state":"resumed"}`)
	add("completed", "r2", KindDone, `{"pending":false}`)
	// resumed but not committed (crash)
	add("resumed", "r3", KindDone, `{"pending":true}`)
	add("resumed", "r3", KindStatus, `{"state":"resumed"}`)
	// plain failure
	add("failed", "r4", KindDone, `{"error":"x","pending":false}`)
	// a pending commit with no pending field at all
	add("nofield", "r5", KindDone, `{}`)

	got, err := st.ListAwaitingRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SessionID != "paused" || got[0].RunID != "r1" {
		t.Fatalf("awaiting = %+v, want only the paused session", got)
	}
}
