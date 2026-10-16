package evaluation

import (
	"context"
	"fmt"
	"testing"
)

type scriptedTurn struct {
	out   string
	tools int
	err   error
}

// scriptedAgent answers turn N of a session with script[N] and records session ids.
type scriptedAgent struct {
	script   []scriptedTurn
	sessions []string
	n        int
}

func (a *scriptedAgent) Generate(ctx context.Context, sessionID, input string) (string, error) {
	out, _, err := a.GenerateMeasured(ctx, sessionID, input)
	return out, err
}

func (a *scriptedAgent) GenerateMeasured(_ context.Context, sessionID, _ string) (string, Metrics, error) {
	a.sessions = append(a.sessions, sessionID)
	s := a.script[a.n]
	a.n++
	return s.out, Metrics{ModelCalls: 1, PromptTokens: 100, ToolCalls: s.tools}, s.err
}

func intp(v int) *int { return &v }

func TestRunCaseMultiTurnSharesSessionAndSumsMetrics(t *testing.T) {
	a := &scriptedAgent{script: []scriptedTurn{{out: "10000", tools: 1}, {out: "1", tools: 1}, {out: "10001"}}}
	c := EvalCase{
		ID: "mt", Input: "q1", ExpectedText: "10000",
		FollowUps: []EvalTurn{{Input: "q2", ExpectedText: `\b1\b`}, {Input: "q3", ExpectedText: "10001"}},
	}
	r := runCase(context.Background(), a, "s1", c)
	if !r.Passed || r.Score != 1 {
		t.Fatalf("want pass, got %+v", r)
	}
	if r.Metrics.ModelCalls != 3 || r.Metrics.PromptTokens != 300 || r.Metrics.ToolCalls != 2 {
		t.Fatalf("metrics not summed over turns: %+v", r.Metrics)
	}
	for _, s := range a.sessions {
		if s != "s1" {
			t.Fatalf("all turns must share the session, got %v", a.sessions)
		}
	}
}

func TestRunCaseStopsAtFirstFailingTurn(t *testing.T) {
	a := &scriptedAgent{script: []scriptedTurn{{out: "10000"}, {out: "wrong"}, {out: "never"}}}
	c := EvalCase{Input: "q1", ExpectedText: "10000",
		FollowUps: []EvalTurn{{Input: "q2", ExpectedText: "42"}, {Input: "q3", ExpectedText: "x"}}}
	r := runCase(context.Background(), a, "s", c)
	if r.Passed || a.n != 2 {
		t.Fatalf("want fail after 2 turns, got passed=%v turns=%d", r.Passed, a.n)
	}
	if r.ErrorMsg == "" {
		t.Fatal("failing multi-turn case must say which turn failed")
	}
}

func TestRunCaseToolCallBounds(t *testing.T) {
	ground := EvalCase{Input: "q", ExpectedText: "ok", MinToolCalls: intp(1)}
	if r := runCase(context.Background(), &scriptedAgent{script: []scriptedTurn{{out: "ok", tools: 0}}}, "s", ground); r.Passed {
		t.Fatal("ungrounded answer must fail min_tool_calls")
	}
	if r := runCase(context.Background(), &scriptedAgent{script: []scriptedTurn{{out: "ok", tools: 2}}}, "s", ground); !r.Passed {
		t.Fatalf("grounded answer must pass: %+v", r)
	}
	plain := EvalCase{Input: "q", ExpectedText: "ok", MaxToolCalls: intp(0)}
	if r := runCase(context.Background(), &scriptedAgent{script: []scriptedTurn{{out: "ok", tools: 1}}}, "s", plain); r.Passed {
		t.Fatal("tool use must fail max_tool_calls=0")
	}
}

func TestRunCaseExpectApproval(t *testing.T) {
	paused := fmt.Errorf("%w: tool %q", ErrApprovalPaused, "execute_bash")
	c := EvalCase{Input: "q", ExpectApproval: true}
	if r := runCase(context.Background(), &scriptedAgent{script: []scriptedTurn{{err: paused}}}, "s", c); !r.Passed {
		t.Fatalf("pausing for approval must pass: %+v", r)
	}
	if r := runCase(context.Background(), &scriptedAgent{script: []scriptedTurn{{out: "done"}}}, "s", c); r.Passed {
		t.Fatal("running a dangerous tool without approval must fail")
	}
	// A pause is a failure for cases that did not ask for it.
	plain := EvalCase{Input: "q", ExpectedText: "ok"}
	if r := runCase(context.Background(), &scriptedAgent{script: []scriptedTurn{{err: paused}}}, "s", plain); r.Passed {
		t.Fatal("unexpected approval pause must fail")
	}
}
