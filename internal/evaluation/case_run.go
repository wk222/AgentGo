package evaluation

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// ErrApprovalPaused is returned by agents when a run stops to wait for a human
// approval. Cases with ExpectApproval treat it as the passing outcome.
var ErrApprovalPaused = errors.New("run paused for approval")

// ApprovalPause is the concrete pause error of agents that expose the approval
// id; it matches ErrApprovalPaused under errors.Is.
type ApprovalPause struct{ ID, Tool string }

func (p *ApprovalPause) Error() string {
	return fmt.Sprintf("%v: tool %q (cases that are not marked expect_approval or decision must not need approval)", ErrApprovalPaused, p.Tool)
}

func (p *ApprovalPause) Unwrap() error { return ErrApprovalPaused }

// ApprovalResolver is an agent that can settle a pause and continue the run it
// stopped. The result is shaped like one Generate turn: a further pause comes
// back as an *ApprovalPause error.
type ApprovalResolver interface {
	ResolveApproval(ctx context.Context, approvalID string, approve bool) (string, Metrics, error)
}

// maxApprovalsPerTurn bounds an approve loop so a model that keeps asking
// cannot hang a case.
const maxApprovalsPerTurn = 12

// settle answers the pauses of one turn according to the case's Decision and
// returns the outcome of the run after the last one.
func settle(ctx context.Context, agent AgentGenerator, c EvalCase, out string, m Metrics, err error) (string, Metrics, error) {
	if c.Decision == "" {
		return out, m, err
	}
	rv, canResolve := agent.(ApprovalResolver)
	for n := 0; n < maxApprovalsPerTurn; n++ {
		var p *ApprovalPause
		if !errors.As(err, &p) {
			return out, m, err
		}
		if !canResolve {
			return out, m, fmt.Errorf("the agent cannot settle approvals (it has no ApprovalResolver): %w", err)
		}
		o, dm, derr := rv.ResolveApproval(ctx, p.ID, c.Decision == "approve")
		m.Add(dm)
		if o != "" {
			out = o
		}
		err = derr
	}
	var p *ApprovalPause
	if errors.As(err, &p) {
		return out, m, fmt.Errorf("still paused after %d approvals", maxApprovalsPerTurn)
	}
	return out, m, err
}

// EvalTurn is a follow-up message sent in the same session as the case input.
type EvalTurn struct {
	Input        string   `json:"input"`
	ExpectedText string   `json:"expected_text,omitempty"`
	Keywords     []string `json:"keywords,omitempty"`
}

type caseRun struct {
	Output   string
	Metrics  Metrics
	Score    float64
	Passed   bool
	ErrorMsg string
}

func (c EvalCase) asTurn() EvalTurn {
	return EvalTurn{Input: c.Input, ExpectedText: c.ExpectedText, Keywords: c.Keywords}
}

// turns returns the case input followed by its follow-ups.
func (c EvalCase) turns() []EvalTurn {
	ts := []EvalTurn{c.asTurn()}
	return append(ts, c.FollowUps...)
}

func generateOnce(ctx context.Context, agent AgentGenerator, sessionID, input string) (string, Metrics, error) {
	if ma, ok := agent.(MeasuredAgent); ok {
		return ma.GenerateMeasured(ctx, sessionID, input)
	}
	out, err := agent.Generate(ctx, sessionID, input)
	return out, Metrics{}, err
}

// transientErrRe matches upstream-provider hiccups (5xx / 429) that say nothing about
// the agent under test. They are retried instead of being scored as failures.
var transientErrRe = regexp.MustCompile(`status code: (5\d\d|429)`)

const maxTransientRetries = 2

// retryBackoff is a variable so tests can run without sleeping.
var retryBackoff = func(attempt int) time.Duration { return time.Duration(attempt) * 3 * time.Second }

func isTransient(err error) bool {
	return err != nil && !errors.Is(err, ErrApprovalPaused) && transientErrRe.MatchString(err.Error())
}

// generateFirstTurn runs the first turn of a run. On a transient upstream error it
// retries in a *fresh* session (the failed attempt may already have persisted the
// user message) and returns the session id that the follow-up turns must reuse.
func generateFirstTurn(ctx context.Context, agent AgentGenerator, sessionID, input string) (string, string, Metrics, error) {
	sid := sessionID
	var total Metrics
	for attempt := 0; ; attempt++ {
		out, m, err := generateOnce(ctx, agent, sid, input)
		total.Add(m)
		if !isTransient(err) || attempt >= maxTransientRetries {
			return sid, out, total, err
		}
		select {
		case <-ctx.Done():
			return sid, out, total, err
		case <-time.After(retryBackoff(attempt + 1)):
		}
		sid = fmt.Sprintf("%s-retry%d", sessionID, attempt+1)
	}
}

// runCase executes one run of a case: every turn goes to the same session, a
// turn is scored on its own expectations, and the run passes only if all turns
// pass. Metrics are summed across turns, so a multi-turn case measures how
// well the prefix stays cached from turn to turn.
func runCase(ctx context.Context, agent AgentGenerator, sessionID string, c EvalCase) caseRun {
	var res caseRun
	cleanup, perr := c.prepareWorkspace()
	defer cleanup()
	if perr != nil {
		res.ErrorMsg = perr.Error()
		return res
	}
	passed := true
	var scoreSum float64
	turns := c.turns()
	for i, t := range turns {
		var out string
		var m Metrics
		var err error
		if i == 0 {
			sessionID, out, m, err = generateFirstTurn(ctx, agent, sessionID, t.Input)
		} else {
			out, m, err = generateOnce(ctx, agent, sessionID, t.Input)
		}
		out, m, err = settle(ctx, agent, c, out, m, err)
		res.Metrics.Add(m)
		res.Output = out
		if err != nil {
			if c.ExpectApproval && errors.Is(err, ErrApprovalPaused) {
				scoreSum++
				continue
			}
			res.ErrorMsg = err.Error()
			passed = false
			break
		}
		if c.ExpectApproval {
			res.ErrorMsg = "expected the run to pause for approval, but it completed"
			passed = false
			break
		}
		score, ok := ScoreCase(out, EvalCase{ExpectedText: t.ExpectedText, Keywords: t.Keywords})
		if t.ExpectedText == "" && len(t.Keywords) == 0 && c.hasVerification() {
			// The disk, not the reply, is what this case asserts.
			score, ok = 1, true
		}
		scoreSum += score
		if !ok {
			passed = false
			if len(turns) > 1 {
				res.ErrorMsg = fmt.Sprintf("turn %d did not match expectations", i+1)
			}
			break
		}
	}
	if passed && c.hasVerification() {
		if err := c.verify(ctx); err != nil {
			passed = false
			res.ErrorMsg = err.Error()
		}
	}
	if passed && c.MaxToolCalls != nil && res.Metrics.ToolCalls > *c.MaxToolCalls {
		passed = false
		res.ErrorMsg = fmt.Sprintf("used %d tool calls, limit %d", res.Metrics.ToolCalls, *c.MaxToolCalls)
	}
	if passed && c.MinToolCalls != nil && res.Metrics.ToolCalls < *c.MinToolCalls {
		passed = false
		res.ErrorMsg = fmt.Sprintf("used %d tool calls, expected at least %d (answer was not grounded in tools)",
			res.Metrics.ToolCalls, *c.MinToolCalls)
	}
	res.Passed = passed
	if passed {
		res.Score = scoreSum / float64(len(turns))
	}
	return res
}
