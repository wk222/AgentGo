package evaluation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type flakyAgent struct {
	failures int // leading calls that fail with a 5xx
	err      error
	sessions []string
}

func (f *flakyAgent) Generate(ctx context.Context, sessionID, input string) (string, error) {
	f.sessions = append(f.sessions, sessionID)
	if len(f.sessions) <= f.failures {
		return "", f.err
	}
	return "42", nil
}

func noSleep(t *testing.T) {
	t.Helper()
	old := retryBackoff
	retryBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { retryBackoff = old })
}

var upstream500 = errors.New("agent error: [NodeRunError] error, status code: 500, status: 500 Internal Server Error")

func TestRunCaseRetriesTransientUpstreamErrors(t *testing.T) {
	noSleep(t)
	agent := &flakyAgent{failures: 2, err: upstream500}
	res := runCase(context.Background(), agent, "s", EvalCase{ID: "c", Input: "q", Keywords: []string{"42"}})
	if !res.Passed || res.ErrorMsg != "" {
		t.Fatalf("expected pass after retries: %+v", res)
	}
	if len(agent.sessions) != 3 {
		t.Fatalf("expected 3 attempts, got %v", agent.sessions)
	}
	// every retry must use a fresh session so a half-persisted first attempt cannot pollute history
	if agent.sessions[0] != "s" || agent.sessions[1] == "s" || agent.sessions[2] == agent.sessions[1] {
		t.Fatalf("sessions: %v", agent.sessions)
	}
}

func TestRunCaseGivesUpAfterMaxRetries(t *testing.T) {
	noSleep(t)
	agent := &flakyAgent{failures: 99, err: upstream500}
	res := runCase(context.Background(), agent, "s", EvalCase{ID: "c", Input: "q", Keywords: []string{"42"}})
	if res.Passed || !strings.Contains(res.ErrorMsg, "status code: 500") {
		t.Fatalf("expected failure: %+v", res)
	}
	if len(agent.sessions) != maxTransientRetries+1 {
		t.Fatalf("attempts: %v", agent.sessions)
	}
}

func TestRunCaseDoesNotRetryRealFailures(t *testing.T) {
	noSleep(t)
	agent := &flakyAgent{failures: 99, err: errors.New("agent error: status code: 400 invalid tool name")}
	res := runCase(context.Background(), agent, "s", EvalCase{ID: "c", Input: "q", Keywords: []string{"42"}})
	if res.Passed || len(agent.sessions) != 1 {
		t.Fatalf("a 400 is a real failure and must not be retried: %+v attempts=%v", res, agent.sessions)
	}
}
