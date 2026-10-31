package governance

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
)

func executionQueue(t *testing.T, path string) *ApprovalQueue {
	t.Helper()
	q, err := NewApprovalQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	return q
}
func executionPolicy() Policy {
	p := BuildPolicy("balanced", "")
	p.Control.MaxCallsPerTool = 0
	p.Control.StuckLoopKillThreshold = 0
	return p
}
func spentUnits(t *testing.T, q *ApprovalQueue) int64 {
	t.Helper()
	var n int64
	if err := q.db.QueryRow(`SELECT COALESCE(SUM(units),0) FROM governance_daily_budget WHERE day=?`, time.Now().Format("2006-01-02")).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestExecutionBudgetPersistsAcrossMiddlewareAndQueueReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	q := executionQueue(t, path)
	p := executionPolicy()
	p.MaxDailyBudget = .01
	runs := 0
	invoke := func(ctx context.Context, _ string) (string, error) { runs++; return "ok", nil }
	ctx := WithExecution(WithSessionID(context.Background(), "same-session"))
	if _, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", invoke); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := executionQueue(t, path)
	// A different root/agent still shares the same host's daily credits.
	other := WithExecution(WithSessionID(context.Background(), "other-session"))
	if _, err := NewGovernanceMiddleware(reopened, p).InvokeWithPolicy(other, "code_view", "{}", invoke); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("budget reset after reopen: %v", err)
	}
	if runs != 1 || spentUnits(t, reopened) != 1 {
		t.Fatalf("calls=%d spent=%d", runs, spentUnits(t, reopened))
	}
}

func TestExecutionApprovalWaitAndVetoNeverSpendOrConsume(t *testing.T) {
	q := executionQueue(t, ":memory:")
	p := executionPolicy()
	p.MaxDailyBudget = 1
	p.ToolRiskLevels["code_view"] = RiskCritical
	ctx := WithCallID(WithExecution(WithSessionID(context.Background(), "s")), "approval-call")
	runs := 0
	action := func(context.Context, string) (string, error) { runs++; return "ok", nil }
	for i := 0; i < 3; i++ {
		if _, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", action); err == nil {
			t.Fatal("did not pause")
		}
	}
	if spentUnits(t, q) != 0 {
		t.Fatal("charged while awaiting approval")
	}
	requests, _ := q.ListPending(ctx, nil)
	if len(requests) != 1 {
		t.Fatal("duplicate pending requests", len(requests))
	}
	id := requests[0].ID
	if err := q.Resolve(ctx, id, true, "ok", "test", &ResumePayload{Approved: true}); err != nil {
		t.Fatal(err)
	}
	remove := RegisterToolHook(ToolHook{Name: "veto", Before: func(context.Context, ToolCall) error { return errors.New("veto fixture") }})
	_, _ = NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", action)
	remove()
	request, _ := q.GetRequest(ctx, id)
	if request.ConsumedAt != nil || runs != 0 || spentUnits(t, q) != 0 {
		t.Fatal("hook veto consumed execution admission", request, runs)
	}
	// An exhausted budget likewise leaves an approved action available.
	if _, err := q.db.Exec(`INSERT INTO governance_daily_budget(day,units) VALUES (?,100) ON CONFLICT(day) DO UPDATE SET units=100`, time.Now().Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", action); err == nil {
		t.Fatal("exhausted budget executed")
	}
	request, _ = q.GetRequest(ctx, id)
	if request.ConsumedAt != nil || runs != 0 {
		t.Fatal("budget failure consumed approval")
	}
	if _, err := q.db.Exec(`UPDATE governance_daily_budget SET units=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", action); err != nil {
		t.Fatal(err)
	}
	if spentUnits(t, q) != 100 || runs != 1 {
		t.Fatal("approved execution charged more than once")
	}
}

func TestRootExecutionCountsShareAcrossNestedSessionsAndAgentRebuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "root.db")
	q := executionQueue(t, path)
	p := executionPolicy()
	p.Control.MaxCallsPerTool = 1
	root := WithExecution(WithSessionID(context.Background(), "parent"))
	runs := 0
	action := func(context.Context, string) (string, error) { runs++; return "", errors.New("actual failed attempt") }
	_, _ = NewGovernanceMiddleware(q, p).InvokeWithPolicy(root, "code_view", "{}", action)
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	q = executionQueue(t, path)
	child := WithExecution(WithSessionID(root, "child"))
	if _, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(child, "code_view", "{}", action); err == nil {
		t.Fatal("child bypassed failed-attempt cap")
	}
	if runs != 1 {
		t.Fatal("failed tool calls did not count", runs)
	}
	// A new turn in the same conversation has its own recent-call scope.
	fresh := WithExecution(WithSessionID(context.Background(), "parent"))
	_, _ = NewGovernanceMiddleware(q, p).InvokeWithPolicy(fresh, "code_view", "{}", action)
	if runs != 2 {
		t.Fatal("next turn inherited the old root's cap")
	}
}

func TestConcurrentExecutionAdmissionAcrossQueueHandles(t *testing.T) {
	for _, rateLimit := range []bool{false, true} {
		t.Run(map[bool]string{false: "daily-budget", true: "root-call-cap"}[rateLimit], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "shared.db")
			queues := []*ApprovalQueue{executionQueue(t, path), executionQueue(t, path)}
			p := executionPolicy()
			if rateLimit {
				p.Control.MaxCallsPerTool = 1
			} else {
				p.MaxDailyBudget = .01
			}
			ctx := WithExecution(WithSessionID(context.Background(), "concurrent"))
			var runs atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					<-start
					_, _ = NewGovernanceMiddleware(queues[i%2], p).InvokeWithPolicy(ctx, "code_view", "{}", func(context.Context, string) (string, error) { runs.Add(1); return "ok", nil })
				}(i)
			}
			close(start)
			wg.Wait()
			if runs.Load() != 1 || spentUnits(t, queues[0]) != 1 {
				t.Fatalf("parallel admission bypass: calls=%d units=%d", runs.Load(), spentUnits(t, queues[0]))
			}
		})
	}
}

func TestInterruptedExecutionReleasesOnlyItsOwnReservation(t *testing.T) {
	q := executionQueue(t, ":memory:")
	p := executionPolicy()
	p.MaxDailyBudget = .01
	ctx := WithExecution(WithSessionID(context.Background(), "nested"))
	m := NewGovernanceMiddleware(q, p)
	_, err := m.InvokeWithPolicy(ctx, "code_view", "{}", func(ctx context.Context, _ string) (string, error) {
		return "", tool.StatefulInterrupt(ctx, "child pending", "state")
	})
	if err == nil || spentUnits(t, q) != 0 {
		t.Fatal("paused parent retained budget", err)
	}
	n, err := q.recentCalls(ctx, executionID(ctx, ""), "code_view")
	if err != nil || n != 0 {
		t.Fatal("paused parent retained call slot", n, err)
	}
	if _, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", func(context.Context, string) (string, error) { return "done", nil }); err != nil {
		t.Fatal(err)
	}
	if spentUnits(t, q) != 1 {
		t.Fatal("completed attempt not charged")
	}
}

func TestApprovalCannotBeBorrowedByAnotherRootWithReusedCallID(t *testing.T) {
	q := executionQueue(t, ":memory:")
	p := executionPolicy()
	p.ToolRiskLevels["code_view"] = RiskHigh
	first := WithCallID(WithExecution(WithSessionID(context.Background(), "same-session")), "reused-call")
	_, _ = NewGovernanceMiddleware(q, p).InvokeWithPolicy(first, "code_view", "{}", func(context.Context, string) (string, error) { t.Fatal("unapproved execution"); return "", nil })
	requests, _ := q.ListPending(first, nil)
	if len(requests) != 1 {
		t.Fatal(requests)
	}
	if err := q.Resolve(first, requests[0].ID, true, "ok", "test", &ResumePayload{Approved: true}); err != nil {
		t.Fatal(err)
	}
	other := WithCallID(WithExecution(WithSessionID(context.Background(), "same-session")), "reused-call")
	runs := 0
	_, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(other, "code_view", "{}", func(context.Context, string) (string, error) { runs++; return "ok", nil })
	if err == nil || runs != 0 {
		t.Fatal("different turn borrowed approval", err, runs)
	}
	request, _ := q.GetRequest(first, requests[0].ID)
	if request.ConsumedAt != nil {
		t.Fatal("different root consumed original approval")
	}
}

func TestExecutionWindowExpiryAndOldDailyRowsStayIndependent(t *testing.T) {
	q := executionQueue(t, ":memory:")
	p := executionPolicy()
	p.Control.MaxCallsPerTool = 1
	ctx := WithExecution(context.Background())
	action := func(context.Context, string) (string, error) { return "ok", nil }
	if _, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", action); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`UPDATE governance_tool_starts SET started_at=?`, time.Now().Add(-executionWindow-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.db.Exec(`INSERT INTO governance_daily_budget(day,units) VALUES ('2000-01-01',1000000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", action); err != nil {
		t.Fatal("expired window still blocks", err)
	}
	if spentUnits(t, q) != 2 {
		t.Fatal("other days interfered with today's budget")
	}
}

func TestAdmissionFailureDoesNotReportAnExecutedHookOutcome(t *testing.T) {
	q := executionQueue(t, ":memory:")
	p := executionPolicy()
	p.MaxDailyBudget = .01
	ctx := WithExecution(context.Background())
	if _, err := q.db.Exec(`INSERT INTO governance_daily_budget(day,units) VALUES (?,1)`, time.Now().Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	before, after, runs := 0, 0, 0
	remove := RegisterToolHook(ToolHook{Name: "admission-fixture", Before: func(context.Context, ToolCall) error { before++; return nil }, After: func(context.Context, ToolCall, ToolOutcome) { after++ }})
	defer remove()
	_, err := NewGovernanceMiddleware(q, p).InvokeWithPolicy(ctx, "code_view", "{}", func(context.Context, string) (string, error) { runs++; return "done", nil })
	if err == nil || before != 1 || after != 0 || runs != 0 || spentUnits(t, q) != 1 {
		t.Fatalf("false execution outcome: before=%d after=%d runs=%d err=%v", before, after, runs, err)
	}
}

func TestCancellationDuringAdmissionNeverEntersEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ran, after := false, false
	remove := RegisterToolHook(ToolHook{Name: "cancel-admission", After: func(context.Context, ToolCall, ToolOutcome) { after = true }})
	defer remove()
	m := NewGovernanceMiddleware(nil, Policy{})
	_, err := m.withHooks("code_view", func(context.Context, string) (string, error) { ran = true; return "done", nil }, func(context.Context, string) error { cancel(); return nil })(ctx, "{}")
	if !errors.Is(err, context.Canceled) || ran || after {
		t.Fatalf("cancelled admission executed: err=%v ran=%v after=%v", err, ran, after)
	}
}
