package memory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/config"
	"agentgo/internal/promptbudget"
)

func learningFixture(t *testing.T) (*Pipeline, context.Context) {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB().Close() })
	return NewPipeline(s), context.Background()
}

const learningOutput = `{"atoms":[{"kind":"preference","topic":"writing","content":"Reports should be concise with sources","quote":"Please write concise reports with sources","visibility":"personal"}]}`

func queueExample(t *testing.T, p *Pipeline, ctx context.Context) string {
	t.Helper()
	id, err := p.QueueLearning(ctx, "session-1", "user_turn", "Please write concise reports with sources", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func TestLearningDurableIdempotentCrossSessionAndBounded(t *testing.T) {
	t.Setenv("AGENTGO_MEMORY_HIERARCHY", "0")
	path := filepath.Join(t.TempDir(), "memory.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPipeline(s)
	ctx := context.Background()
	id := queueExample(t, p, ctx)
	if id != queueExample(t, p, ctx) {
		t.Fatal("not idempotent")
	}
	s.DB().Close()
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB().Close()
	p = NewPipeline(s)
	calls := 0
	p.SetLearningCaller(func(context.Context, string, string) (string, error) { calls++; return learningOutput, nil })
	for i := 0; i < 3; i++ {
		if err = p.ProcessLearning(ctx, 4); err != nil {
			t.Fatal(err)
		}
	}
	status, err := p.LearningStatus(ctx)
	if err != nil || status["done"] != 1 || calls != 1 {
		t.Fatal(status, calls, err)
	}
	rows, err := p.Recall(ctx, "reports", RecallOptions{Scope: "global", Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if rows[0].IsCanonical || rows[0].SourceTrust != 0.35 || rows[0].Metadata["source_quote"] == nil {
		t.Fatal("unsafe promotion", rows[0])
	}
	prompt, err := p.ContextPrompt(WithPromptBudget(WithRecallQuery(ctx, "reports"), 120), "different-session")
	if err != nil || !strings.Contains(prompt, "Reports should be concise") || promptbudget.Estimate(prompt) > 120 {
		t.Fatal(prompt, err)
	}
	// Forgetting evidence invalidates learned children and cannot be undone by re-delivery.
	if _, err = s.DB().Exec(`UPDATE memories SET status='forgotten' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRecord(ctx, rows[0].ID)
	if err != nil || got.Status != "archived" {
		t.Fatal(got, err)
	}
	if _, err = p.QueueLearning(ctx, "session-1", "user_turn", "Please write concise reports with sources", "", ""); err == nil {
		t.Fatal("revived withdrawn source")
	}
}
func TestLearningRejectsUnsupportedQuotesAndDocumentFacts(t *testing.T) {
	p, ctx := learningFixture(t)
	queueExample(t, p, ctx)
	if _, err := p.QueueLearning(ctx, "s", "document", "Example revenue is 999; use short sections", "policy.docx", "report"); err != nil {
		t.Fatal(err)
	}
	p.SetLearningCaller(func(context.Context, string, string) (string, error) {
		return `{"atoms":[{"kind":"preference","content":"Assistant says everything works","quote":"invented source quotation","visibility":"personal"},{"kind":"fact","content":"Revenue is always 999","quote":"Example revenue is 999","visibility":"personal"}]}`, nil
	})
	if err := p.ProcessLearning(ctx, 4); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := p.store.db.QueryRow(`SELECT count(*) FROM memories WHERE modality='fact'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
}
func TestLearningRetryAndNoCallerDoNotLoseWork(t *testing.T) {
	p, ctx := learningFixture(t)
	queueExample(t, p, ctx)
	if err := p.ProcessLearning(ctx, 1); err != nil {
		t.Fatal(err)
	}
	status, _ := p.LearningStatus(ctx)
	if status["pending"] != 1 {
		t.Fatal(status)
	}
	p.SetLearningCaller(func(context.Context, string, string) (string, error) {
		return "", errors.New("failure with sensitive content")
	})
	if err := p.ProcessLearning(ctx, 1); err == nil {
		t.Fatal("expected error")
	}
	var stored string
	if err := p.store.db.QueryRow(`SELECT last_error FROM memory_learning_jobs`).Scan(&stored); err != nil || stored != "extraction_failed" {
		t.Fatal(stored, err)
	}
	if _, err := p.store.db.Exec(`UPDATE memory_learning_jobs SET next_attempt=0`); err != nil {
		t.Fatal(err)
	}
	p.SetLearningCaller(func(context.Context, string, string) (string, error) { return learningOutput, nil })
	if err := p.ProcessLearning(ctx, 1); err != nil {
		t.Fatal(err)
	}
	status, _ = p.LearningStatus(ctx)
	if status["done"] != 1 {
		t.Fatal(status)
	}
}
func TestLearningDoesNotReviveWithdrawnCandidate(t *testing.T) {
	p, ctx := learningFixture(t)
	queueExample(t, p, ctx)
	p.SetLearningCaller(func(context.Context, string, string) (string, error) { return learningOutput, nil })
	if err := p.ProcessLearning(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.db.Exec(`UPDATE memories SET status='forgotten' WHERE modality='fact'`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.db.Exec(`UPDATE memory_learning_jobs SET state='pending'`); err != nil {
		t.Fatal(err)
	}
	if err := p.ProcessLearning(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var n int
	p.store.db.QueryRow(`SELECT count(*) FROM memories WHERE modality='fact' AND status='active'`).Scan(&n)
	if n != 0 {
		t.Fatal("revived candidate")
	}
}
func TestSchedulerDiscoversActualSessionsAndPersistsCadence(t *testing.T) {
	t.Setenv("AGENTGO_AUTO_LEARN", "0")
	p, ctx := learningFixture(t)
	for _, scope := range []string{"chat-a", "chat-b"} {
		if err := p.Ingest(ctx, Record{ID: scope, Scope: scope, Modality: "episode", Content: "assistant report not verified successful execution"}); err != nil {
			t.Fatal(err)
		}
	}
	scheduler := NewDistillScheduler(p, nil, func() int { return 1 })
	scheduler.tick(ctx)
	// Recreating the scheduler must preserve per-session cadence, not create fresh summaries.
	NewDistillScheduler(p, nil, nil).tick(ctx)
	var n int
	if err := p.store.db.QueryRow(`SELECT count(*) FROM memories WHERE modality='reflection'`).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	var empty int
	p.store.db.QueryRow(`SELECT count(*) FROM memories WHERE scope='mode:assistant'`).Scan(&empty)
	if empty != 0 {
		t.Fatal(empty)
	}
	// Config is read live. Disabled learning must leave queued work pending.
	queueExample(t, p, ctx)
	p.SetLearningCaller(func(context.Context, string, string) (string, error) { t.Fatal("learning disabled"); return "", nil })
	if config.AutoLearn.On() {
		t.Fatal("flag not disabled")
	}
	scheduler.tick(ctx)
}

func TestLearningExplicitCorrectionPreservesHumanEndorsement(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(map[bool]string{false: "automatic", true: "endorsed"}[pinned], func(t *testing.T) {
			p, ctx := learningFixture(t)
			queueExample(t, p, ctx)
			p.SetLearningCaller(func(context.Context, string, string) (string, error) { return learningOutput, nil })
			if err := p.ProcessLearning(ctx, 1); err != nil {
				t.Fatal(err)
			}
			rows, err := p.Recall(ctx, "reports", RecallOptions{Scope: "global"})
			if err != nil || len(rows) != 1 {
				t.Fatal(rows, err)
			}
			old := rows[0]
			if pinned {
				if err = p.store.MarkCanonical(ctx, old.ID, true); err != nil {
					t.Fatal(err)
				}
			}
			text := "Correction: I now want detailed reports with sources"
			if _, err = p.QueueLearning(ctx, "session-1", "user_turn", text, "", ""); err != nil {
				t.Fatal(err)
			}
			p.SetLearningCaller(func(context.Context, string, string) (string, error) {
				return `{"atoms":[{"kind":"preference","topic":"writing","content":"Reports should be detailed with sources","quote":"Correction: I now want detailed reports with sources","visibility":"personal","replaces":"` + old.ID + `"}]}`, nil
			})
			if err = p.ProcessLearning(ctx, 1); err != nil {
				t.Fatal(err)
			}
			got, err := p.store.GetRecord(ctx, old.ID)
			if err != nil {
				t.Fatal(err)
			}
			if pinned && got.Status != "active" {
				t.Fatal("changed human endorsement")
			}
			if !pinned && (got.Status != "archived" || got.SupersedesID == "") {
				t.Fatal("did not version correction", got)
			}
		})
	}
}

func TestLearningReclaimsExpiredLeaseAndRespectsSourceDeletion(t *testing.T) {
	p, ctx := learningFixture(t)
	id := queueExample(t, p, ctx)
	if _, err := p.store.db.Exec(`UPDATE memory_learning_jobs SET state='running',next_attempt=0`); err != nil {
		t.Fatal(err)
	}
	p.SetLearningCaller(func(context.Context, string, string) (string, error) { return learningOutput, nil })
	if err := p.ProcessLearning(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.db.Exec(`DELETE FROM memories WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := p.store.db.QueryRow(`SELECT count(*) FROM memories WHERE modality='fact' AND status='active'`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	status, err := p.LearningStatus(ctx)
	if err != nil || status["cancelled"] != 1 {
		t.Fatal(status, err)
	}
}

func TestLearningDocumentStyleIsReusableButNotCanonical(t *testing.T) {
	p, ctx := learningFixture(t)
	if _, err := p.QueueLearning(ctx, "office", "document", "Use short headings and cite the source at the end.", "sample.docx", "report"); err != nil {
		t.Fatal(err)
	}
	p.SetLearningCaller(func(context.Context, string, string) (string, error) {
		return `{"atoms":[{"kind":"style","topic":"report","content":"Use short headings and source citations","quote":"Use short headings and cite the source","visibility":"personal"}]}`, nil
	})
	if err := p.ProcessLearning(ctx, 1); err != nil {
		t.Fatal(err)
	}
	rows, err := p.Recall(ctx, "headings", RecallOptions{Scope: "global"})
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if rows[0].IsCanonical || rows[0].Metadata["source_ref"] != "sample.docx" {
		t.Fatal(rows[0])
	}
}

func TestLearningWithdrawalDuringEmbeddingCannotInsertOrphan(t *testing.T) {
	p, ctx := learningFixture(t)
	id := queueExample(t, p, ctx)
	p.SetLearningCaller(func(context.Context, string, string) (string, error) { return learningOutput, nil })
	p.SetIngestHook(func(ctx context.Context, rec Record) error {
		// Simulate a slow embedding call during which another request deletes evidence.
		if _, err := p.store.db.ExecContext(ctx, `DELETE FROM memories WHERE id=?`, id); err != nil {
			return err
		}
		return p.store.Ingest(ctx, rec)
	})
	if err := p.ProcessLearning(ctx, 1); err == nil {
		t.Fatal("expected withdrawn evidence rejection")
	}
	var n int
	p.store.db.QueryRow(`SELECT count(*) FROM memories WHERE modality='fact'`).Scan(&n)
	if n != 0 {
		t.Fatal("inserted orphan memory")
	}
}
