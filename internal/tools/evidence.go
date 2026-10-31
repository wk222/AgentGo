package tools

import (
	"agentgo/internal/evidence"
	"agentgo/internal/governance"
	"context"
	"github.com/cloudwego/eino/components/tool/utils"
)

type EvidenceCitation = evidence.Citation
type evidenceRecord = evidence.Record

func WithEvidenceRun(ctx context.Context) context.Context {
	return evidence.WithRun(ctx, governance.SessionIDFromContext(ctx))
}

// evidenceStreak counts consecutive misses of one kind in this run.
func evidenceStreak(ctx context.Context, key string, miss bool) int {
	return evidence.Streak(ctx, governance.SessionIDFromContext(ctx), key, miss)
}
func evidenceStreakCount(ctx context.Context, key string) int {
	return evidence.StreakCount(ctx, governance.SessionIDFromContext(ctx), key)
}
func registerEvidence(ctx context.Context, c EvidenceCitation, text string) *EvidenceCitation {
	return evidence.Register(ctx, governance.SessionIDFromContext(ctx), c, text)
}
func readEvidence(ctx context.Context, id string) (evidenceRecord, error) {
	return evidence.Read(ctx, governance.SessionIDFromContext(ctx), id)
}

func registerReadEvidence(r *Registry) error {
	t, err := utils.InferTool("read_evidence", "Retrieve the exact untrusted source excerpt behind a citation ID from this run, without reopening a file or fetching a live page. Historical IDs are unavailable; read the original source again. Citation syntax in answers is [[ev_ID]] using the complete citation.id returned by parse_file/web_fetch. Never invent IDs or claim unread text supports an answer.",
		func(ctx context.Context, in struct {
			ID string `json:"id" jsonschema:"required,description=Exact citation.id returned by a source read in this run."`
		}) (evidenceRecord, error) {
			return readEvidence(ctx, in.ID)
		})
	if err != nil {
		return err
	}
	Declare(Spec{Name: "read_evidence", Source: "builtin", Risk: governance.RiskLow})
	r.AddTool(t)
	return nil
}
