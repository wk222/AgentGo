package bridge

import (
	"context"
	"strings"

	"agentgo/internal/core"
	"agentgo/internal/engine"
	"agentgo/internal/interactive"
)

// PendingQuestionDTO is an ask_user pause that nobody has answered yet. It
// lives in the pending-run store (and survives a restart), not in the approval
// queue, so ListPendingApprovals does not show it even though it blocks a
// workspace switch exactly like an approval does.
type PendingQuestionDTO struct {
	ID        string
	SessionID string
	Prompt    string // the question, when this process still has it; else the user's request
}

func isQuestionRun(p core.PendingRun) bool { return p.ToolName == "ask_user" }

// ListPendingQuestions lists the unanswered ask_user pauses.
func (s *AppService) ListPendingQuestions() []PendingQuestionDTO {
	var out []PendingQuestionDTO
	for _, p := range s.rt.pending.Snapshot() {
		if !isQuestionRun(p) {
			continue
		}
		q := PendingQuestionDTO{ID: p.ApprovalID, SessionID: p.SessionID, Prompt: strings.TrimSpace(p.UserText)}
		for _, id := range []string{p.ApprovalID, p.InterruptID} {
			if pl, ok := interactive.Get(id); ok && strings.TrimSpace(pl.Prompt) != "" {
				q.Prompt = strings.TrimSpace(pl.Prompt)
				break
			}
		}
		out = append(out, q)
	}
	return out
}

// DiscardPendingQuestion drops an unanswered question without resuming its
// run, closing the run in the log so it does not stay "awaiting" forever.
func (s *AppService) DiscardPendingQuestion(id string) bool {
	p, ok := s.rt.pending.Take(id)
	if !ok || !isQuestionRun(p) {
		if ok {
			s.rt.pending.Set(p) // not ours: put it back untouched
		}
		return false
	}
	runID := core.AwaitingRunID(context.Background(), s.rt.Sessions(), p)
	s.commitRunWith(p.SessionID, runID, engine.RunResult{}, map[string]any{"resolution": "discarded"})
	return true
}
