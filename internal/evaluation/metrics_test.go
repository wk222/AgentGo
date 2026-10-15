package evaluation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func sseServer(t *testing.T, body string, wantToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chat/stream" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if wantToken != "" && r.Header.Get("Authorization") != "Bearer "+wantToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPAgentReadsOutputAndUsage(t *testing.T) {
	srv := sseServer(t, strings.Join([]string{
		"event: chunk", `data: {"delta":"ab"}`, "",
		"event: chunk", `data: {"delta":"c"}`, "",
		"event: done", `data: {"content":"abc","usage":{"model_calls":2,"prompt_tokens":300,"cached_tokens":200,"completion_tokens":40,"reasoning_tokens":5,"tool_calls":3}}`, "",
	}, "\n"), "tok")

	a := &HTTPAgent{BaseURL: srv.URL, Token: "tok"}
	out, m, err := a.GenerateMeasured(context.Background(), "s1", "hi")
	if err != nil {
		t.Fatal(err)
	}
	if out != "abc" {
		t.Errorf("output = %q", out)
	}
	want := Metrics{ModelCalls: 2, PromptTokens: 300, CachedTokens: 200, CompletionTokens: 40, ReasoningTokens: 5, ToolCalls: 3}
	if m != want {
		t.Errorf("metrics = %+v, want %+v", m, want)
	}
	if got := m.CacheHitRate(); got < 0.666 || got > 0.667 {
		t.Errorf("hit rate = %v", got)
	}

	if _, _, err := (&HTTPAgent{BaseURL: srv.URL, Token: "wrong"}).GenerateMeasured(context.Background(), "s", "x"); err == nil ||
		!strings.Contains(err.Error(), "401") {
		t.Errorf("wrong token should be rejected with 401, got %v", err)
	}
}

func TestHTTPAgentSurfacesErrorsAndInterrupts(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"error event": {"event: error\ndata: {\"error\":\"boom\"}\n\n", "boom"},
		"interrupt": {"event: interrupt\ndata: {\"tool_name\":\"rm_rf\"}\n\n" +
			"event: done\ndata: {\"content\":\"waiting\"}\n\n", "approval"},
		"truncated": {"event: chunk\ndata: {\"delta\":\"x\"}\n\n", "without a done"},
	}
	for name, tc := range cases {
		srv := sseServer(t, tc.body, "")
		_, _, err := (&HTTPAgent{BaseURL: srv.URL}).GenerateMeasured(context.Background(), "s", "x")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want containing %q", name, err, tc.want)
		}
	}
}

type fakeMeasured struct{ tools int }

func (f fakeMeasured) Generate(ctx context.Context, sid, in string) (string, error) {
	o, _, e := f.GenerateMeasured(ctx, sid, in)
	return o, e
}
func (f fakeMeasured) GenerateMeasured(_ context.Context, _, _ string) (string, Metrics, error) {
	return "391", Metrics{PromptTokens: 100, CachedTokens: 50, ToolCalls: f.tools}, nil
}

func TestMaxToolCallsAndTotals(t *testing.T) {
	zero := 0
	set := EvalSet{ID: "s", Cases: []EvalCase{{ID: "plain", Input: "q", ExpectedText: "391", MaxToolCalls: &zero}}}

	res, err := EvaluateSet(context.Background(), fakeMeasured{tools: 0}, set, 2, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OverallPassed || res.TotalRuns != 2 || res.Totals.PromptTokens != 200 || res.Totals.CachedTokens != 100 {
		t.Errorf("clean run: %+v", res)
	}

	res, err = EvaluateSet(context.Background(), fakeMeasured{tools: 1}, set, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.OverallPassed || !strings.Contains(res.Cases[0].Details[0].ErrorMsg, "tool calls") {
		t.Errorf("a no-tools case that used a tool must fail: %+v", res.Cases[0].Details[0])
	}
}

type sessionSpy struct{ ids []string }

func (s *sessionSpy) Generate(_ context.Context, sid, _ string) (string, error) {
	s.ids = append(s.ids, sid)
	return "ok", nil
}

func TestTagKeepsSessionsUniquePerEvaluation(t *testing.T) {
	set := EvalSet{ID: "s", Cases: []EvalCase{{ID: "c", Input: "q", ExpectedText: "ok"}}}
	spy := &sessionSpy{}
	for _, tag := range []string{"A", "B"} {
		if _, err := EvaluateSetTagged(context.Background(), spy, set, 1, false, tag); err != nil {
			t.Fatal(err)
		}
	}
	if len(spy.ids) != 2 || spy.ids[0] == spy.ids[1] {
		t.Fatalf("session ids must differ between evaluations: %v", spy.ids)
	}
}

func result(rate float64, prompt, cached, runs int) *EvalResult {
	return &EvalResult{PassingRate: rate, TotalRuns: runs, Totals: Metrics{PromptTokens: prompt, CachedTokens: cached}}
}

func TestCompare(t *testing.T) {
	th := DefaultThresholds()
	base := result(1.0, 8000, 6000, 8)

	if fs := Compare(base, result(1.0, 8000, 6000, 8), th); len(fs) != 0 {
		t.Errorf("identical results must not report anything: %+v", fs)
	}
	if fs := Compare(base, result(0.75, 8000, 6000, 8), th); !HasFailure(fs) {
		t.Errorf("a 25-point pass-rate drop must fail: %+v", fs)
	}
	if fs := Compare(base, result(0.95, 8000, 6000, 8), th); HasFailure(fs) {
		t.Errorf("a 5-point drop is within the limit: %+v", fs)
	}
	if fs := Compare(base, result(1.0, 10400, 7800, 8), th); !HasFailure(fs) {
		t.Errorf("+30%% prompt tokens must fail: %+v", fs)
	}
	// Different run counts: compare per run, not totals.
	if fs := Compare(base, result(1.0, 16000, 12000, 16), th); len(fs) != 0 {
		t.Errorf("same per-run cost with doubled runs must pass: %+v", fs)
	}
	fs := Compare(base, result(1.0, 8000, 2000, 8), th)
	if HasFailure(fs) || len(fs) != 1 || !strings.Contains(fs[0].Msg, "cache hit rate") {
		t.Errorf("a cache-hit drop is a warning only: %+v", fs)
	}
	if !HasFailure(Compare(nil, base, th)) {
		t.Error("missing baseline must fail loudly")
	}
}

func TestCompareNamesRegressedCases(t *testing.T) {
	base := &EvalResult{PassingRate: 1, Cases: []CaseSummary{{CaseID: "a", Passed: true}, {CaseID: "b", Passed: true}}}
	cur := &EvalResult{PassingRate: 0.95, Cases: []CaseSummary{{CaseID: "a", Passed: true}, {CaseID: "b", Passed: false}}}
	fs := Compare(base, cur, DefaultThresholds())
	if len(fs) != 1 || !strings.Contains(fs[0].Msg, "b") {
		t.Fatalf("regressed case must be named: %+v", fs)
	}
}

func TestDefaultSuiteIsWellFormed(t *testing.T) {
	set, err := DefaultSuite()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Cases) < 8 {
		t.Fatalf("default suite has %d cases", len(set.Cases))
	}
	seen := map[string]bool{}
	for _, c := range set.Cases {
		if c.ID == "" || c.Input == "" || seen[c.ID] {
			t.Errorf("bad or duplicate case %q", c.ID)
		}
		seen[c.ID] = true
		if c.ExpectedText == "" && len(c.Keywords) == 0 {
			t.Errorf("%s has no scoring criterion", c.ID)
		}
		if c.ExpectedText != "" {
			if _, err := regexp.Compile(c.ExpectedText); err != nil {
				t.Errorf("%s: expected_text is not a valid regexp: %v", c.ID, err)
			}
		}
	}
}
