package evaluation

import (
	"context"
	"errors"
	"fmt"
)

// ErrApprovalPaused is returned by agents when a run stops to wait for a human
// approval. Cases with ExpectApproval treat it as the passing outcome.
var ErrApprovalPaused = errors.New("run paused for approval")

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

// runCase executes one run of a case: every turn goes to the same session, a
// turn is scored on its own expectations, and the run passes only if all turns
// pass. Metrics are summed across turns, so a multi-turn case measures how
// well the prefix stays cached from turn to turn.
func runCase(ctx context.Context, agent AgentGenerator, sessionID string, c EvalCase) caseRun {
	var res caseRun
	passed := true
	var scoreSum float64
	turns := c.turns()
	for i, t := range turns {
		out, m, err := generateOnce(ctx, agent, sessionID, t.Input)
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
		scoreSum += score
		if !ok {
			passed = false
			if len(turns) > 1 {
				res.ErrorMsg = fmt.Sprintf("turn %d did not match expectations", i+1)
			}
			break
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
