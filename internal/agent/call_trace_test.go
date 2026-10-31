package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"

	"agentgo/internal/governance"
)

func traceOn(t *testing.T) {
	t.Helper()
	SetCallTraceEnabled(true)
	ClearCallTrace("")
	t.Cleanup(func() {
		callTraceMode.Store(0)
		ClearCallTrace("")
	})
}

// chain builds the production order: safe tool (outermost) -> argument repair -> governance -> tool.
func traceChain(t *testing.T, name, callID string, tool_ func(string) (string, error)) func(context.Context, string) (string, error) {
	t.Helper()
	q, err := governance.NewApprovalQueue(filepath.Join(t.TempDir(), "approvals.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = q.Close() })
	gov := governance.NewGovernanceMiddleware(q, governance.BuildPolicy("balanced", t.TempDir()))
	tc := &adk.ToolContext{Name: name, CallID: callID}
	var ep adk.InvokableToolCallEndpoint = func(ctx context.Context, args string, _ ...tool.Option) (string, error) { return tool_(args) }
	var err2 error
	if ep, err2 = gov.WrapInvokableToolCall(context.Background(), ep, tc); err2 != nil {
		t.Fatal(err2)
	}
	if ep, err2 = NewADKToolHealMiddleware().WrapInvokableToolCall(context.Background(), ep, tc); err2 != nil {
		t.Fatal(err2)
	}
	if ep, err2 = NewSafeToolMiddleware().WrapInvokableToolCall(context.Background(), ep, tc); err2 != nil {
		t.Fatal(err2)
	}
	return func(ctx context.Context, args string) (string, error) { return ep(ctx, args) }
}

func stages(events []CallTraceEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Stage)
	}
	return out
}

func TestCallTraceIsOffByDefaultAndRecordsNothing(t *testing.T) {
	ClearCallTrace("")
	callTraceMode.Store(0)
	call := traceChain(t, "code_view", "c0", func(string) (string, error) { return "ok", nil })
	if _, err := call(BindRunContext(context.Background(), "off"), `{}`); err != nil {
		t.Fatal(err)
	}
	if got := CallTraceFor("off", CallTraceFilter{}); len(got) != 0 {
		t.Fatalf("tracing is off but %d steps were kept", len(got))
	}
}

func TestCallTraceFollowsOneCallFromModelArgsToResult(t *testing.T) {
	traceOn(t)
	call := traceChain(t, "code_view", "call-7", func(args string) (string, error) {
		if !strings.Contains(args, `"path"`) {
			t.Fatalf("tool did not get the arguments: %s", args)
		}
		return "file body with token=abcdef123456789", nil
	})
	ctx := BindRunContext(context.Background(), "agentic:s1")
	raw := `{"path":"a.go",}` // a trailing comma the model wrote; repair fixes it before the tool runs
	if _, err := call(ctx, raw); err != nil {
		t.Fatal(err)
	}
	events := CallTraceFor("s1", CallTraceFilter{CallID: "call-7"})
	got := stages(events)
	want := []string{TraceStageModelArgs, TraceStageArgsRepaired}
	if len(got) < 4 || got[0] != want[0] || got[1] != want[1] || got[len(got)-1] != TraceStageResult {
		t.Fatalf("stages = %v, want model_args, args_repaired, policy..., result", got)
	}
	hasPolicy := false
	for _, e := range events {
		if e.CallID != "call-7" || e.RunID == "" || e.RunID != RunIDFromContext(ctx) || e.Tool != "code_view" && e.Stage != TraceStagePolicy {
			t.Fatalf("event does not carry the call/run/tool: %+v", e)
		}
		hasPolicy = hasPolicy || e.Stage == TraceStagePolicy
	}
	if !hasPolicy {
		t.Fatalf("no policy decision in %v", got)
	}
	if events[0].Data["args"] != raw {
		t.Fatalf("model_args must be the arguments exactly as sent, got %v", events[0].Data["args"])
	}
	if after, _ := events[1].Data["after"].(string); strings.Contains(after, ",}") {
		t.Fatalf("repaired arguments still broken: %s", after)
	}
	last := events[len(events)-1]
	if res, _ := last.Data["result"].(string); strings.Contains(res, "abcdef123456789") || !strings.Contains(res, "***") {
		t.Fatalf("a credential reached the trace: %q", res)
	}
}

func TestCallTraceKeepsFailuresAndFilters(t *testing.T) {
	traceOn(t)
	bad := traceChain(t, "code_view", "c-bad", func(string) (string, error) { return "", errors.New("boom") })
	good := traceChain(t, "code_view", "c-good", func(string) (string, error) { return "fine", nil })
	ctx := BindRunContext(context.Background(), "s2")
	_, _ = bad(ctx, `{}`)
	_, _ = good(BindRunContext(context.Background(), "s2"), `{}`)
	events := CallTraceFor("s2", CallTraceFilter{CallID: "c-bad"})
	last := events[len(events)-1]
	if last.Stage != TraceStageResult || last.Data["failed"] != true {
		t.Fatalf("failure not kept: %+v", last)
	}
	for _, e := range events {
		if e.CallID == "c-good" {
			t.Fatal("filter by call id leaked another call")
		}
	}
	if len(CallTraceFor("s2", CallTraceFilter{})) <= len(events) {
		t.Fatal("unfiltered trace must hold both calls")
	}
}

func TestCallTraceRunLevelStepsAccompanyTheCall(t *testing.T) {
	traceOn(t)
	ctx := BindRunContext(context.Background(), "s3")
	recordCallTrace(ctx, "", "", TraceStageUsage, 0, map[string]any{"prompt_tokens": 10})
	recordCallTrace(ctx, "k1", "t", TraceStageResult, 0, nil)
	other := BindRunContext(context.Background(), "s3")
	recordCallTrace(other, "", "", TraceStageCompaction, 0, map[string]any{"applied": true})
	got := stages(CallTraceFor("s3", CallTraceFilter{CallID: "k1"}))
	if len(got) != 2 || got[0] != TraceStageUsage || got[1] != TraceStageResult {
		t.Fatalf("a call should come with its own run's usage but not another run's compaction: %v", got)
	}
}

func TestCallTraceIsBounded(t *testing.T) {
	traceOn(t)
	ctx := BindRunContext(context.Background(), "big")
	for i := 0; i < callTraceMaxEvents+50; i++ {
		recordCallTrace(ctx, "c", "t", TraceStageResult, 0, map[string]any{"result": strings.Repeat("x", callTraceMaxRunes*3)})
	}
	got := CallTraceFor("big", CallTraceFilter{})
	if len(got) != callTraceMaxEvents {
		t.Fatalf("kept %d steps, want %d", len(got), callTraceMaxEvents)
	}
	if n := len([]rune(got[0].Data["result"].(string))); n > callTraceMaxRunes+40 {
		t.Fatalf("text not clipped: %d runes", n)
	}
	for i := 0; i < callTraceMaxSessions+3; i++ {
		recordCallTrace(BindRunContext(context.Background(), "x"+string(rune('a'+i))), "c", "t", TraceStageResult, 0, nil)
	}
	if n := len(CallTraceSessions()); n != callTraceMaxSessions {
		t.Fatalf("kept %d sessions, want %d", n, callTraceMaxSessions)
	}
}
