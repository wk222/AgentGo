package bridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"agentgo/internal/checkpoint"
	"agentgo/internal/governance"
	"agentgo/internal/sessions"

	_ "modernc.org/sqlite"
)

// A pid that no process has: well above anything the OS hands out.
const deadPID = 2147480000

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("this process is alive")
	}
	if processAlive(deadPID) {
		t.Fatal("pid that does not exist reported alive")
	}
	if processAlive(0) || processAlive(-1) {
		t.Fatal("invalid pids are not alive")
	}
}

// withCheckpoint gives the rig a checkpoint store holding a checkpoint for the
// rig's session, as an interrupted agent run leaves behind.
func (g *resumeRig) withCheckpoint(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	cp, err := checkpoint.OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := cp.Set(context.Background(), checkpoint.CheckpointIDForSession(g.sid), []byte("state")); err != nil {
		t.Fatal(err)
	}
	g.s.rt.cpStore = cp
}

// crash simulates the process dying: memory is gone, the database keeps its
// rows, and they are owned by a process that no longer exists.
func (g *resumeRig) crash(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	rows, err := g.s.rt.Sessions().ListPendingRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		r.OwnerPID = deadPID
		if err := g.s.rt.Sessions().SavePendingRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	g.s.rt.pending = newDurablePendingStore(g.s.rt.Sessions()) // fresh memory
}

func (g *resumeRig) rows(t *testing.T) []sessions.PendingRun {
	t.Helper()
	rows, err := g.s.rt.Sessions().ListPendingRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPendingRunsArePersistedAndReleasedWithTheStore(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	rows := g.rows(t)
	if len(rows) != 1 || rows[0].ApprovalID != id || rows[0].OwnerPID != os.Getpid() || rows[0].SessionID != g.sid {
		t.Fatalf("rows = %+v", rows)
	}
	var pr pendingRun
	if err := json.Unmarshal([]byte(rows[0].PayloadJSON), &pr); err != nil || pr.InterruptID != "ic-"+id || pr.ToolName != "code_edit" {
		t.Fatalf("payload = %s (%v)", rows[0].PayloadJSON, err)
	}
	if _, ok := g.s.rt.pending.Take(id); !ok {
		t.Fatal("take")
	}
	if len(g.rows(t)) != 0 {
		t.Fatal("a taken run must leave the database too")
	}
	g.s.rt.pending.Set(pendingRun{ApprovalID: "x", SessionID: g.sid})
	g.s.rt.pending.Delete("x")
	if len(g.rows(t)) != 0 {
		t.Fatal("delete must leave the database too")
	}
}

// The whole point: the process restarts, and the user can still approve.
func TestRestartThenApproveStillResumesTheRun(t *testing.T) {
	g := newResumeRig(t)
	g.withCheckpoint(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	g.crash(t)

	adopted, closed := g.s.rt.RecoverPending(context.Background())
	if adopted != 1 || closed != 0 {
		t.Fatalf("adopted=%d closed=%d", adopted, closed)
	}
	if _, ok := g.s.rt.pending.Get(id); !ok {
		t.Fatal("run must be resumable again after the restart")
	}
	if rows := g.rows(t); len(rows) != 1 || rows[0].OwnerPID != os.Getpid() {
		t.Fatalf("the adopted run must be owned by this process now: %+v", rows)
	}
	if st := g.state(t); st.Status != sessions.RunAwaitingApproval {
		t.Fatalf("an adopted run is still waiting: %+v", st)
	}

	out := resolve(g, context.Background(), id, true)
	if out["success"] != true || out["resume_error"] != nil {
		t.Fatalf("approve after restart: %+v", out)
	}
	if g.calls.Load() != 1 {
		t.Fatalf("resumed %d times, want 1", g.calls.Load())
	}
	if st := g.state(t); st.Status != sessions.RunCompleted || st.RunID != "run_a" {
		t.Fatalf("after approval: %+v", st)
	}
}

func TestRecoveryLeavesRunsOfLiveProcessesAlone(t *testing.T) {
	g := newResumeRig(t)
	g.withCheckpoint(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	// Another live process (the test's parent) owns it.
	ctx := context.Background()
	rows := g.rows(t)
	rows[0].OwnerPID = os.Getppid()
	if err := g.s.rt.Sessions().SavePendingRun(ctx, rows[0]); err != nil {
		t.Fatal(err)
	}
	g.s.rt.pending = newDurablePendingStore(g.s.rt.Sessions())

	adopted, closed := g.s.rt.RecoverPending(ctx)
	if adopted != 0 || closed != 0 {
		t.Fatalf("touched a live process's run: adopted=%d closed=%d", adopted, closed)
	}
	if r := g.rows(t); len(r) != 1 || r[0].ApprovalID != id || r[0].OwnerPID != os.Getppid() {
		t.Fatalf("row changed: %+v", r)
	}
	if st := g.state(t); st.Status != sessions.RunAwaitingApproval {
		t.Fatalf("its log run must stay awaiting: %+v", st)
	}
	if g.s.rt.pending.Len() != 0 {
		t.Fatal("must not adopt a live process's run")
	}
}

func TestRecoveryClosesARunWhoseCheckpointIsGone(t *testing.T) {
	g := newResumeRig(t)
	// A checkpoint store that has nothing for this session.
	db, _ := sql.Open("sqlite", ":memory:")
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	cp, err := checkpoint.OpenSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	g.s.rt.cpStore = cp
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	g.crash(t)

	adopted, closed := g.s.rt.RecoverPending(context.Background())
	if adopted != 0 || closed != 1 {
		t.Fatalf("adopted=%d closed=%d", adopted, closed)
	}
	st := g.state(t)
	if st.Status != sessions.RunFailed || !strings.Contains(st.Error, "检查点") {
		t.Fatalf("state = %+v", st)
	}
	if len(g.rows(t)) != 0 || g.s.rt.pending.Len() != 0 {
		t.Fatal("an unresumable run must be dropped")
	}
	// Its approval card must not stay open: nobody can act on it.
	if open, _ := g.queue.ListPending(context.Background(), nil); len(open) != 0 {
		t.Fatalf("approval %s still open", id)
	}
	out := resolve(g, context.Background(), id, true)
	if out["success"] != false {
		t.Fatalf("a closed approval cannot be approved: %+v", out)
	}
}

func TestRecoveryNeverReExecutesAnApprovedButUnappliedDecision(t *testing.T) {
	g := newResumeRig(t)
	g.withCheckpoint(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	// The decision reached the queue, then the process died before resuming.
	if err := g.queue.Resolve(context.Background(), id, true, "ok", "tester", &governance.ResumePayload{Approved: true}); err != nil {
		t.Fatal(err)
	}
	g.crash(t)

	adopted, closed := g.s.rt.RecoverPending(context.Background())
	if adopted != 0 || closed != 1 {
		t.Fatalf("adopted=%d closed=%d", adopted, closed)
	}
	if g.calls.Load() != 0 {
		t.Fatal("recovery must never execute a tool")
	}
	st := g.state(t)
	if st.Status != sessions.RunFailed || st.Error != reasonInterrupted {
		t.Fatalf("state = %+v", st)
	}
}

func TestRecoveryClosesARejectedRunAsRejected(t *testing.T) {
	g := newResumeRig(t)
	g.withCheckpoint(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	if err := g.queue.Resolve(context.Background(), id, false, "no", "tester", &governance.ResumePayload{}); err != nil {
		t.Fatal(err)
	}
	g.crash(t)
	g.s.rt.RecoverPending(context.Background())
	if st := g.state(t); st.Status != sessions.RunCompleted {
		t.Fatalf("a refused run is closed, not failed: %+v", st)
	}
}

func TestRecoveryClosesAPausedRunNothingRefersTo(t *testing.T) {
	g := newResumeRig(t)
	g.startRun(t, "run_a") // log says paused; there is no pending run at all
	adopted, closed := g.s.rt.RecoverPending(context.Background())
	if adopted != 0 || closed != 1 {
		t.Fatalf("adopted=%d closed=%d", adopted, closed)
	}
	if st := g.state(t); st.Status != sessions.RunFailed || st.Error != reasonLost {
		t.Fatalf("state = %+v", st)
	}
	// Running it again changes nothing.
	if a, c := g.s.rt.RecoverPending(context.Background()); a != 0 || c != 0 {
		t.Fatalf("second pass adopted=%d closed=%d", a, c)
	}
}

func TestRecoveryAdoptsAnAskUserQuestionWithoutAQueueRow(t *testing.T) {
	g := newResumeRig(t)
	g.withCheckpoint(t)
	// ask_user questions are answered through AnswerQuestion and never enter the approval queue.
	g.s.rt.pending.Set(pendingRun{
		SessionID: g.sid, ToolName: "ask_user", ApprovalID: "q1", InterruptID: "q1",
	})
	g.startRun(t, "run_a")
	g.crash(t)
	adopted, closed := g.s.rt.RecoverPending(context.Background())
	if adopted != 1 || closed != 0 {
		t.Fatalf("adopted=%d closed=%d", adopted, closed)
	}
}

func TestRecoveryDropsAnUnreadableRow(t *testing.T) {
	g := newResumeRig(t)
	ctx := context.Background()
	if err := g.s.rt.Sessions().SavePendingRun(ctx, sessions.PendingRun{
		ApprovalID: "bad", SessionID: g.sid, OwnerPID: deadPID, PayloadJSON: "{not json",
	}); err != nil {
		t.Fatal(err)
	}
	g.startRun(t, "run_a")
	adopted, closed := g.s.rt.RecoverPending(ctx)
	if adopted != 0 || closed != 1 || len(g.rows(t)) != 0 {
		t.Fatalf("adopted=%d closed=%d rows=%d", adopted, closed, len(g.rows(t)))
	}
	if st := g.state(t); st.Status != sessions.RunFailed {
		t.Fatalf("state = %+v", st)
	}
}

// expireApproval makes the queue treat id as stale (created before the cutoff).
func (g *resumeRig) expireAll(t *testing.T) {
	t.Helper()
	time.Sleep(1100 * time.Millisecond) // created_at has whole-second precision
	n, err := g.queue.ExpirePendingOlderThan(context.Background(), time.Millisecond)
	if err != nil || n == 0 {
		t.Fatalf("expire n=%d err=%v", n, err)
	}
}

func TestExpiredApprovalReleasesItsRunAndUnblocksWorkspaceSwitching(t *testing.T) {
	g := newResumeRig(t)
	expiring := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	g.expireAll(t)

	// A second approval that is still open, and one whose decision is being applied.
	open := g.pause(t, "code_write")
	deciding := g.pause(t, "code_multiedit")
	if err := g.queue.Resolve(context.Background(), deciding, true, "ok", "t", &governance.ResumePayload{Approved: true}); err != nil {
		t.Fatal(err)
	}

	if g.s.rt.pending.Len() != 3 {
		t.Fatalf("setup: %d pending", g.s.rt.pending.Len())
	}
	g.s.rt.sweepExpiredPending(context.Background())

	if _, ok := g.s.rt.pending.Get(expiring); ok {
		t.Fatal("expired run must be released")
	}
	if _, ok := g.s.rt.pending.Get(open); !ok {
		t.Fatal("an open approval must be kept")
	}
	if _, ok := g.s.rt.pending.Get(deciding); !ok {
		t.Fatal("a decision being applied belongs to resolveApproval, not the sweep")
	}
	if st := g.state(t); st.Status != sessions.RunFailed || st.Error != reasonExpired {
		t.Fatalf("state = %+v", st)
	}
	if len(g.rows(t)) != 2 {
		t.Fatalf("rows = %d, want 2", len(g.rows(t)))
	}
	// Nothing was executed.
	if g.calls.Load() != 0 {
		t.Fatal("expiry must not resume anything")
	}
}
