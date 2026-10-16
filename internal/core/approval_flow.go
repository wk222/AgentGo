package core

import (
	"context"
	"errors"

	"agentgo/internal/governance"
)

// ErrNoPendingRun is reported when an approval was granted but nothing is
// waiting to be resumed — typically because the process restarted (pending runs
// live in memory) or the decision was already handled.
var ErrNoPendingRun = errors.New("没有可恢复的运行（进程已重启，或该审批已被处理）；工具没有被执行")

// ApprovalResolver records a decision on an approval. *governance.ApprovalQueue
// implements it.
type ApprovalResolver interface {
	Resolve(ctx context.Context, id string, approved bool, note, resolvedBy string, resume *governance.ResumePayload) error
}

// ApprovalFlow is the kernel half of "a user decided on an approval": record the
// decision once, tell surfaces waiting on it, and claim the right to resume the
// paused run atomically. What happens to the claimed run (resuming an agent,
// matrix or workflow, and showing the outcome) is the caller's job.
type ApprovalFlow struct {
	Approvals ApprovalResolver
	Pending   *PendingStore
	Relay     *ApprovalRelay
}

// Decision is what Decide hands back to the caller once a decision is recorded.
type Decision struct {
	Resume *governance.ResumePayload // payload to pass to the resumed run
	Run    PendingRun                // the claimed run (Arguments already carry finalArgs)
	Found  bool                      // false: nothing was waiting to be resumed
}

// Decide records the decision and claims the pending run.
//
// ok is false when the decision could not be recorded; out is then the final
// reply ({"success": false, "error": ...}) and complete is nil. Otherwise out
// starts as {"success": true} and the caller adds resume_error, messages, ...
// to it; complete must be called (defer it) once out is final, so the waiting
// surfaces receive the outcome. When approved but nothing was waiting, out
// already carries resume_error and Found is false.
func (f *ApprovalFlow) Decide(ctx context.Context, approvalID string, approved bool, note, resolvedBy, finalArgs string) (d Decision, out map[string]any, complete func(), ok bool) {
	if err := ctx.Err(); err != nil {
		return d, map[string]any{"success": false, "error": err.Error()}, nil, false
	}
	d.Resume = &governance.ResumePayload{Approved: approved, Arguments: finalArgs}
	if err := f.Approvals.Resolve(ctx, approvalID, approved, note, resolvedBy, d.Resume); err != nil {
		return d, map[string]any{"success": false, "error": err.Error()}, nil, false
	}
	// The decision is on record: let any surface waiting on it close its dialog,
	// and give it the outcome once the caller is done with the resume.
	f.Relay.MarkDecided(approvalID, approved, resolvedBy)
	out = map[string]any{"success": true}
	complete = func() { f.Relay.MarkCompleted(approvalID, out) }

	// Claim the right to resume. Only one caller can ever get the pending run.
	d.Run, d.Found = f.Pending.Take(approvalID)
	if !d.Found {
		if approved {
			out["resume_error"] = ErrNoPendingRun.Error()
		}
		return d, out, complete, true
	}
	if finalArgs != "" {
		d.Run.Arguments = finalArgs
	}
	return d, out, complete, true
}
