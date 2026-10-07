package bridge

import (
	"context"
	"encoding/json"
	"log"
	"os"

	"agentgo/internal/checkpoint"
	"agentgo/internal/engine"
	"agentgo/internal/sessions"
)

// Runs that stop for a decision are kept in pending_runs (see pending.go), and
// the agent's checkpoint is durable, so a restart does not have to lose them.
// This file decides what becomes of such a run when its owner is gone or its
// approval can no longer be given.
//
// The rule that shapes everything here: recovery never executes a tool. A run
// that cannot be resumed from the user's own decision is closed with an explicit
// reason; it is not retried. That is what keeps "at most one execution per tool
// call" true across crashes.

const (
	reasonExpired     = "审批已过期；工具没有被执行"
	reasonMissing     = "找不到该审批请求；工具没有被执行"
	reasonInterrupted = "审批已批准，但进程在恢复运行前停止；工具没有被重新执行，请重新发起任务"
	reasonLost        = "进程在等待审批时停止，且待恢复的运行已丢失；工具没有被执行"
)

// resumabilityProblem says why a pending run could never be resumed, or "" when
// it looks resumable. It only looks at what resuming needs: the interrupt id and
// the agent's checkpoint.
func (rt *Runtime) resumabilityProblem(ctx context.Context, pr pendingRun) string {
	if pr.ResumeKind == "workflow" {
		return "" // resumed through the workflow engine, which validates its own checkpoint
	}
	if pr.InterruptID == "" {
		return "该审批没有对应的检查点标识，无法恢复；工具没有被执行"
	}
	if rt.cpStore == nil {
		return ""
	}
	_, ok, err := rt.cpStore.Get(ctx, checkpoint.CheckpointIDForSession(pr.SessionID))
	if err != nil {
		return "" // cannot tell; leave it to the resume to fail loudly
	}
	if !ok {
		return "检查点已不存在，无法恢复；工具没有被执行"
	}
	return ""
}

// judgePending decides the fate of a pending run whose owner is gone. keep is
// true when the run can still be resumed by a decision; otherwise res/extra
// describe how its log run is closed.
//
// An ask_user question is not in the approval queue by design, so a missing
// request is normal for it.
func (rt *Runtime) judgePending(ctx context.Context, pr pendingRun) (keep bool, res engine.RunResult, extra map[string]any) {
	failed := func(reason string) (bool, engine.RunResult, map[string]any) {
		return false, engine.RunResult{Error: reason}, map[string]any{"resolution": "recovered"}
	}
	status := "pending"
	if q := rt.Approvals(); q != nil && pr.ToolName != "ask_user" {
		req, err := q.GetRequest(ctx, pr.ApprovalID)
		if err != nil {
			return true, engine.RunResult{}, nil // cannot tell: do not destroy anything
		}
		if req == nil {
			status = "missing"
		} else {
			status = req.Status
		}
	}
	switch status {
	case "pending":
		if reason := rt.resumabilityProblem(ctx, pr); reason != "" {
			// It can never be resumed, so do not leave an approval card open for it.
			if q := rt.Approvals(); q != nil && pr.ToolName != "ask_user" {
				_ = q.Resolve(ctx, pr.ApprovalID, false, "recovery: "+reason, "system", nil)
			}
			return failed(reason)
		}
		return true, engine.RunResult{}, nil
	case "rejected":
		return false, engine.RunResult{}, map[string]any{"resolution": "rejected"}
	case "expired":
		return failed(reasonExpired)
	case "approved":
		// The decision was recorded but the process stopped before the run
		// resumed. Whether the tool ran is unknown, so it is not run again.
		return failed(reasonInterrupted)
	default:
		return failed(reasonMissing)
	}
}

// closeAwaitingRun commits the session's log run, but only if the log shows it
// paused for approval: anything else is not ours to close.
func (rt *Runtime) closeAwaitingRun(ctx context.Context, sessionID string, res engine.RunResult, extra map[string]any) {
	st := rt.Sessions()
	if st == nil || sessionID == "" {
		return
	}
	state, err := st.LastRunState(ctx, sessionID)
	if err != nil || state.Status != sessions.RunAwaitingApproval {
		return
	}
	rt.commitRunWith(sessionID, state.RunID, res, extra)
}

// RecoverPending runs once at startup. It adopts runs that a dead process left
// waiting (so they can still be approved), closes the ones that cannot be
// resumed, and closes paused runs that no pending run refers to any more.
// Rows owned by a live process are never touched: desktop, TUI and headless
// processes share one database.
func (rt *Runtime) RecoverPending(ctx context.Context) (adopted, closed int) {
	st := rt.Sessions()
	if st == nil || rt.pending == nil {
		return 0, 0
	}
	rows, err := st.ListPendingRuns(ctx)
	if err != nil {
		log.Printf("[recover] list pending runs: %v", err)
		return 0, 0
	}
	self := os.Getpid()
	for _, row := range rows {
		if row.OwnerPID == self || processAlive(row.OwnerPID) {
			continue
		}
		var pr pendingRun
		if err := json.Unmarshal([]byte(row.PayloadJSON), &pr); err != nil || pr.ApprovalID == "" {
			// Without its description the run cannot be resumed.
			log.Printf("[recover] pending run %s is unreadable (%v); dropping it", row.ApprovalID, err)
			_ = st.DeletePendingRun(ctx, row.ApprovalID)
			rt.closeAwaitingRun(ctx, row.SessionID, engine.RunResult{Error: reasonMissing}, map[string]any{"resolution": "recovered"})
			closed++
			continue
		}
		keep, res, extra := rt.judgePending(ctx, pr)
		if keep {
			rt.pending.Set(pr) // re-saves it under this process
			adopted++
			continue
		}
		_ = st.DeletePendingRun(ctx, row.ApprovalID)
		rt.closeAwaitingRun(ctx, pr.SessionID, res, extra)
		closed++
	}

	// Paused runs with nothing left to resume them: the process stopped between
	// taking the pending run and finishing, or the row was lost.
	left, err := st.ListPendingRuns(ctx)
	if err != nil {
		return adopted, closed
	}
	held := make(map[string]bool, len(left))
	for _, p := range left {
		held[p.SessionID] = true
	}
	awaiting, err := st.ListAwaitingRuns(ctx)
	if err != nil {
		log.Printf("[recover] list awaiting runs: %v", err)
		return adopted, closed
	}
	for _, a := range awaiting {
		if held[a.SessionID] {
			continue
		}
		rt.commitRunWith(a.SessionID, a.RunID, engine.RunResult{Error: reasonLost}, map[string]any{"resolution": "recovered"})
		closed++
	}
	if adopted+closed > 0 {
		log.Printf("[recover] pending runs: adopted=%d closed=%d", adopted, closed)
	}
	return adopted, closed
}

// sweepExpiredPending lets go of runs this process holds whose approval has
// expired: they can never be decided any more, and a held run blocks workspace
// switching. It only acts on expiry; a decision that is being applied right now
// shows up as approved/rejected and is left to resolveApproval.
func (rt *Runtime) sweepExpiredPending(ctx context.Context) {
	q := rt.Approvals()
	if q == nil || rt.pending == nil {
		return
	}
	for _, pr := range rt.pending.Snapshot() {
		if pr.ToolName == "ask_user" {
			continue
		}
		req, err := q.GetRequest(ctx, pr.ApprovalID)
		if err != nil || req == nil || req.Status != "expired" {
			continue
		}
		// Claim it like a decision would, so a racing decision and this sweep
		// cannot both act on the run.
		if _, ok := rt.pending.Take(pr.ApprovalID); !ok {
			continue
		}
		rt.closeAwaitingRun(ctx, pr.SessionID, engine.RunResult{Error: reasonExpired}, map[string]any{"resolution": "expired"})
		log.Printf("[recover] released pending run %s: %s", pr.ApprovalID, reasonExpired)
	}
}
