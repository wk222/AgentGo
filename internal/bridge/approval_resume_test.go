package bridge

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"agentgo/internal/agent"
	"agentgo/internal/engine"
	"agentgo/internal/governance"
	"agentgo/internal/sessions"
)

// resumeRig is a service with a real approval queue, pending store and session
// log, and a scripted resume instead of an LLM.
type resumeRig struct {
	s     *AppService
	queue *governance.ApprovalQueue
	sid   string
	calls atomic.Int32
	// script is called with the 1-based call number.
	script func(ctx context.Context, n int, pr pendingRun) (*agent.RunResult, error)
}

func newResumeRig(t *testing.T) *resumeRig {
	t.Helper()
	s, sid := newLoggedService(t, runLogEngine{})
	queue, err := governance.NewApprovalQueue(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = queue.Close() })
	s.rt.approvals = queue
	s.rt.pending = newDurablePendingStore(s.rt.Sessions())
	g := &resumeRig{s: s, queue: queue, sid: sid}
	s.rt.resumeFn = func(ctx context.Context, pr pendingRun, _ *governance.ResumePayload) (*agent.RunResult, error) {
		n := int(g.calls.Add(1))
		if g.script == nil {
			return &agent.RunResult{Content: "resumed", UsedTools: true}, nil
		}
		return g.script(ctx, n, pr)
	}
	return g
}

// pause registers an approval as the first turn of a run would: queue row,
// pending run, and a run in the log that stopped waiting for it.
func (g *resumeRig) pause(t *testing.T, tool string) string {
	t.Helper()
	req := governance.NewApprovalRequest(string(governance.KindToolApproval), "test", "Approve "+tool, "risk")
	if err := g.queue.CreateRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	g.s.rt.pending.Set(pendingRun{
		SessionID: g.sid, UserText: "do it", ToolName: tool, Arguments: `{}`,
		ApprovalID: req.ID, InterruptID: "ic-" + req.ID,
	})
	return req.ID
}

// startRun puts a run that is paused for approval into the log.
func (g *resumeRig) startRun(t *testing.T, runID string) {
	t.Helper()
	if _, err := g.s.rt.Sessions().AppendRunEvent(context.Background(), sessions.RunEvent{
		SessionID: g.sid, RunID: runID, Kind: sessions.KindStatus, PayloadJSON: `{"state":"started"}`,
	}); err != nil {
		t.Fatal(err)
	}
	g.s.commitRun(g.sid, runID, engine.RunResult{Pending: true})
}

func (g *resumeRig) state(t *testing.T) sessions.RunState {
	t.Helper()
	st, err := g.s.rt.Sessions().LastRunState(context.Background(), g.sid)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (g *resumeRig) logKinds(t *testing.T) string {
	t.Helper()
	evs, err := g.s.rt.Sessions().ListRunEvents(context.Background(), g.sid, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return kinds(evs)
}

func resolve(g *resumeRig, ctx context.Context, id string, approved bool) map[string]any {
	return g.s.resolveApproval(ctx, id, approved, "test", "tester", "")
}

func TestResolveApprovalChainsTwoApprovals(t *testing.T) {
	g := newResumeRig(t)
	first := g.pause(t, "code_edit")
	g.startRun(t, "run_a")

	secondID := ""
	g.script = func(_ context.Context, n int, pr pendingRun) (*agent.RunResult, error) {
		if n == 1 {
			// The resumed run does some work and stops for a second approval.
			req := governance.NewApprovalRequest(string(governance.KindToolApproval), "test", "Approve execute_bash", "risk")
			if err := g.queue.CreateRequest(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			secondID = req.ID
			return &agent.RunResult{Content: "edited a.go", UsedTools: true, PendingApproval: &agent.PendingApproval{
				ApprovalID: req.ID, InterruptID: "ic2", ToolName: "execute_bash", Arguments: `{"command":"go test"}`,
			}}, nil
		}
		if pr.ApprovalID != secondID || pr.InterruptID != "ic2" {
			t.Errorf("second resume got wrong pending run: %+v", pr)
		}
		return &agent.RunResult{Content: "tests pass", UsedTools: true}, nil
	}

	out := resolve(g, context.Background(), first, true)
	if out["success"] != true || out["resume_error"] != nil {
		t.Fatalf("first resolve: %+v", out)
	}
	msgs, _ := out["messages"].([]ChatMessageDTO)
	if len(msgs) != 2 || msgs[1].Type != "approval" || msgs[1].Status != "pending" || msgs[1].ApprovalID != secondID {
		t.Fatalf("first resolve must surface the second approval, got %+v", msgs)
	}
	if _, ok := g.s.rt.pending.Get(secondID); !ok {
		t.Fatal("second approval was not registered as resumable")
	}
	if st := g.state(t); st.Status != sessions.RunAwaitingApproval || st.RunID != "run_a" {
		t.Fatalf("run must be awaiting again after the second pause: %+v", st)
	}

	out = resolve(g, context.Background(), secondID, true)
	if out["success"] != true || out["resume_error"] != nil {
		t.Fatalf("second resolve: %+v", out)
	}
	if st := g.state(t); st.Status != sessions.RunCompleted || st.RunID != "run_a" {
		t.Fatalf("run must complete after the last approval: %+v", st)
	}
	if got := g.calls.Load(); got != 2 {
		t.Fatalf("each approval resumes exactly once, got %d resumes", got)
	}

	// The transcript holds the text and the second approval card, in order.
	rows, _ := g.s.rt.Sessions().GetMessages(context.Background(), g.sid, 50)
	var types []string
	for _, m := range rows {
		types = append(types, m.Type+":"+m.Content)
	}
	got := strings.Join(types, "|")
	for _, want := range []string{"text:edited a.go", "approval:等待审批: execute_bash", "text:tests pass"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript missing %q: %s", want, got)
		}
	}
	// A chained pause is now also a durable approval fact, before its commit.
	if k := g.logKinds(t); k != "status,done,status,approval_request,done,status,done" {
		t.Fatalf("run log = %s", k)
	}
}

func TestResolveApprovalDuplicateDecisionResumesOnce(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")

	if out := resolve(g, context.Background(), id, true); out["success"] != true {
		t.Fatalf("first: %+v", out)
	}
	out := resolve(g, context.Background(), id, true)
	if out["success"] != false {
		t.Fatalf("a second decision must be refused, got %+v", out)
	}
	if got := g.calls.Load(); got != 1 {
		t.Fatalf("resumed %d times, want 1", got)
	}
}

func TestResolveApprovalConcurrentDecisionsResumeOnce(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")

	var wg sync.WaitGroup
	var wins atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if out := resolve(g, context.Background(), id, true); out["success"] == true {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || g.calls.Load() != 1 {
		t.Fatalf("decisions accepted=%d resumes=%d, want 1 and 1", wins.Load(), g.calls.Load())
	}
}

func TestResolveApprovalRejectClosesRunWithoutResuming(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")

	out := resolve(g, context.Background(), id, false)
	if out["success"] != true || out["resume_error"] != nil {
		t.Fatalf("reject: %+v", out)
	}
	if g.calls.Load() != 0 {
		t.Fatal("a refused tool call must not resume the run")
	}
	if _, ok := g.s.rt.pending.Get(id); ok {
		t.Fatal("pending run must be cleared")
	}
	if st := g.state(t); st.Status != sessions.RunCompleted {
		t.Fatalf("a refused run must not stay awaiting approval: %+v", st)
	}
	evs, _ := g.s.rt.Sessions().ListRunEvents(context.Background(), g.sid, 0, 0)
	if last := evs[len(evs)-1]; !strings.Contains(last.PayloadJSON, `"resolution":"rejected"`) {
		t.Fatalf("commit should say how it was resolved: %s", last.PayloadJSON)
	}
}

func TestResolveApprovalCancelledWhileWaitingKeepsApprovalOpen(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := resolve(g, ctx, id, true)
	if out["success"] != false {
		t.Fatalf("a cancelled caller must not record a decision: %+v", out)
	}
	if _, ok := g.s.rt.pending.Get(id); !ok {
		t.Fatal("the run must still be resumable by a later decision")
	}
	open, _ := g.queue.ListPending(context.Background(), nil)
	if len(open) != 1 {
		t.Fatalf("approval must stay open, %d pending", len(open))
	}
	if g.calls.Load() != 0 {
		t.Fatal("nothing may resume")
	}
	// A later decision still works.
	if out := resolve(g, context.Background(), id, true); out["success"] != true || g.calls.Load() != 1 {
		t.Fatalf("later decision: %+v calls=%d", out, g.calls.Load())
	}
}

func TestResolveApprovalCancelledDuringResumeFailsTheRun(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")

	ctx, cancel := context.WithCancel(context.Background())
	g.script = func(ctx context.Context, _ int, _ pendingRun) (*agent.RunResult, error) {
		cancel() // the user cancels while the resumed run is working
		<-ctx.Done()
		return nil, ctx.Err()
	}
	out := resolve(g, ctx, id, true)
	if e, _ := out["resume_error"].(string); !strings.Contains(e, "canceled") {
		t.Fatalf("cancellation must be reported, got %+v", out)
	}
	st := g.state(t)
	if st.Status != sessions.RunFailed || !strings.Contains(st.Error, "canceled") {
		t.Fatalf("run state = %+v", st)
	}
	if _, ok := g.s.rt.pending.Get(id); ok {
		t.Fatal("a resume that was attempted must not be offered again: the tool may have run")
	}
}

func TestResolveApprovalWithoutPendingRunFailsLoudly(t *testing.T) {
	g := newResumeRig(t)
	req := governance.NewApprovalRequest(string(governance.KindToolApproval), "test", "Approve x", "risk")
	if err := g.queue.CreateRequest(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	// No pending run: what a restart leaves behind, since pending runs live in memory.
	out := resolve(g, context.Background(), req.ID, true)
	if out["success"] != true {
		t.Fatalf("the decision itself is recorded: %+v", out)
	}
	if e, _ := out["resume_error"].(string); e != errNoPendingRun.Error() {
		t.Fatalf("approved-but-nothing-ran must be an error, got %+v", out)
	}
	if g.calls.Load() != 0 {
		t.Fatal("nothing may resume")
	}
}

func TestResolveApprovalResumeFailureFailsTheRun(t *testing.T) {
	g := newResumeRig(t)
	id := g.pause(t, "code_edit")
	g.startRun(t, "run_a")
	g.script = func(context.Context, int, pendingRun) (*agent.RunResult, error) {
		return nil, errors.New("checkpoint not found")
	}
	out := resolve(g, context.Background(), id, true)
	if out["resume_error"] != "checkpoint not found" {
		t.Fatalf("got %+v", out)
	}
	if st := g.state(t); st.Status != sessions.RunFailed || st.Error != "checkpoint not found" {
		t.Fatalf("run state = %+v", st)
	}
}

func TestResolveApprovalWithoutRunnerOrInterruptIsAnError(t *testing.T) {
	g := newResumeRig(t)
	g.s.rt.resumeFn = nil // use the production path; there is no agent runner
	id := g.pause(t, "code_edit")
	out := resolve(g, context.Background(), id, true)
	if e, _ := out["resume_error"].(string); e == "" {
		t.Fatalf("missing runner must be reported: %+v", out)
	}
}
