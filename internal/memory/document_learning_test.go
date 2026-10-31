package memory

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

const docTextA = "Use sections and short paragraphs. Cite original sources clearly."
const docTextB = "Group content by subject and retain dates in headings."

func queueDocument(t *testing.T, p *Pipeline, source, text, expected string) DocumentLearningResult {
	t.Helper()
	r, err := p.ReplaceDocumentLearning(context.Background(), "s", source, "writing", text, expected)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func documentAtom(t *testing.T, p *Pipeline, sourceID, atomID string) Record {
	t.Helper()
	src, err := p.store.GetRecord(context.Background(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	r := Record{ID: atomID, Scope: "global", Modality: "fact", Content: "Use short structured paragraphs", Status: "active", Importance: .7, SourceTrust: .35,
		Metadata: map[string]interface{}{"source": "personal_learning", "memory_kind": "style", "source_ids": []string{sourceID}, "source_version": stableDerivedMemoryID("learnversion", "", src.Content), "source_quote": "Use sections and short paragraphs"}}
	if err = p.Ingest(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDocumentReplacementIdempotentRetractionAndScope(t *testing.T) {
	p, ctx := learningFixture(t)
	a := queueDocument(t, p, "report.docx", docTextA, "")
	atom := documentAtom(t, p, a.SourceIDs[0], "derived")
	revBefore, err := p.ContextRevision(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	other, err := p.ReplaceDocumentLearning(ctx, "other", "report.docx", "writing", docTextA, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Ingest(ctx, Record{ID: "independent", Scope: "global", Modality: "fact", Content: "Independently confirmed fact", Status: "active", IsCanonical: true}); err != nil {
		t.Fatal(err)
	}
	retry := queueDocument(t, p, "report.docx", docTextA, a.Revision)
	if retry.Changed || retry.SourceIDs[0] != a.SourceIDs[0] {
		t.Fatal("identical import was requeued")
	}
	b := queueDocument(t, p, "report.docx", docTextB, a.Revision)
	for _, id := range []string{a.SourceIDs[0], atom.ID} {
		rec, err := p.store.GetRecord(ctx, id)
		if err != nil || rec.Status != "archived" {
			t.Fatalf("stale record %s: %+v %v", id, rec, err)
		}
	}
	if err = p.Ingest(ctx, atom); err == nil {
		t.Fatal("late source atom was republished")
	}
	var state string
	if err = p.store.db.QueryRow(`SELECT state FROM memory_learning_jobs WHERE source_id=?`, a.SourceIDs[0]).Scan(&state); err != nil || state != "cancelled" {
		t.Fatalf("old job %s %v", state, err)
	}
	for _, id := range []string{other.SourceIDs[0], "independent"} {
		rec, err := p.store.GetRecord(ctx, id)
		if err != nil || rec.Status != "active" {
			t.Fatal("unrelated source withdrawn")
		}
	}
	revAfter, err := p.ContextRevision(ctx, "s")
	if err != nil || revBefore == revAfter {
		t.Fatal("context cache revision did not change")
	}
	h := &HybridEngine{sqlite: p.store}
	visible, err := h.currentRecallRecords(ctx, []Record{atom, {ID: b.SourceIDs[0]}, {ID: "independent"}}, "")
	if err != nil || len(visible) != 2 {
		t.Fatalf("stale vector hit survived: %+v %v", visible, err)
	}
	if _, err = p.RetractDocumentLearning(ctx, "s", "report.docx", a.Revision); err == nil {
		t.Fatal("stale deletion accepted")
	}
	deleted, err := p.RetractDocumentLearning(ctx, "s", "report.docx", b.Revision)
	if err != nil || !deleted.Deleted || !deleted.Changed {
		t.Fatal(deleted, err)
	}
	if _, err = p.ReplaceDocumentLearning(ctx, "s", "report.docx", "writing", docTextA, b.Revision); err == nil {
		t.Fatal("pre-delete revision reactivated source")
	}
	again, err := p.RetractDocumentLearning(ctx, "s", "report.docx", deleted.Revision)
	if err != nil || again.Changed {
		t.Fatal("repeated delete changed content", again, err)
	}
	back := queueDocument(t, p, "report.docx", docTextA, deleted.Revision)
	if back.SourceIDs[0] == a.SourceIDs[0] {
		t.Fatal("ABA resurrected old worker generation")
	}
	old, err := p.store.GetRecord(ctx, a.SourceIDs[0])
	if err != nil || old.Status != "archived" {
		t.Fatal("old evidence revived")
	}
}

func TestDocumentReplacementRollsBackAllSections(t *testing.T) {
	p, ctx := learningFixture(t)
	a := queueDocument(t, p, "doc", docTextA, "")
	documentAtom(t, p, a.SourceIDs[0], "atom")
	_, err := p.store.db.Exec(`CREATE TRIGGER fail_document_section BEFORE INSERT ON memory_learning_jobs
 WHEN (SELECT json_extract(metadata,'$.document_section') FROM memories WHERE id=NEW.source_id)=1
 BEGIN SELECT RAISE(ABORT,'injected section failure'); END;`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.ReplaceDocumentLearning(ctx, "s", "doc", "writing", strings.Repeat("中文格式与段落。", 1800), a.Revision)
	if err == nil {
		t.Fatal("injected failure was ignored")
	}
	for _, id := range []string{a.SourceIDs[0], "atom"} {
		r, err := p.store.GetRecord(ctx, id)
		if err != nil || r.Status != "active" {
			t.Fatal("old version lost on failed replacement")
		}
	}
	var revision string
	if err = p.store.db.QueryRow(`SELECT revision FROM memory_documents WHERE scope='s' AND source='doc'`).Scan(&revision); err != nil || revision != a.Revision {
		t.Fatal("partial version published")
	}
	var jobs int
	if err = p.store.db.QueryRow(`SELECT count(*) FROM memory_learning_jobs`).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatal("partial job queue escaped rollback")
	}
}

func TestDocumentConcurrentCASAndLateExtraction(t *testing.T) {
	p, ctx := learningFixture(t)
	a := queueDocument(t, p, "doc", docTextA, "")
	entered, release := make(chan struct{}), make(chan struct{})
	p.SetLearningCaller(func(context.Context, string, string) (string, error) {
		close(entered)
		<-release
		return `{"atoms":[{"kind":"style","topic":"writing","content":"Use short paragraphs","quote":"Use sections and short paragraphs","visibility":"personal"}]}`, nil
	})
	done := make(chan error, 1)
	go func() { done <- p.ProcessLearning(ctx, 1) }()
	<-entered
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, text := range []string{docTextB, "New document with a completely different style."} {
		wg.Add(1)
		go func(text string) {
			defer wg.Done()
			_, err := p.ReplaceDocumentLearning(ctx, "s", "doc", "writing", text, a.Revision)
			results <- err
		}(text)
	}
	wg.Wait()
	close(results)
	success, stale := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if strings.Contains(err.Error(), "source_changed") {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if success != 1 || stale != 1 {
		t.Fatalf("CAS winners %d stale %d", success, stale)
	}
	var count int
	if err := p.store.db.QueryRow(`SELECT count(*) FROM memories WHERE status='active' AND json_extract(metadata,'$.source')='personal_learning'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("late extraction published stale atoms")
	}
}

func TestDocumentLegacySourcesAndFreshGenerationCanLearnAgain(t *testing.T) {
	p, ctx := learningFixture(t)
	for _, ref := range []string{"doc#section-1", "doc#section-not-a-number", "doc-other#section-1"} {
		r := Record{ID: ref, Scope: "global", Modality: "journal", Content: docTextA, Status: "active", Metadata: map[string]interface{}{"source": "personal_learning_evidence", "source_kind": "document", "origin_scope": "s", "source_ref": ref, "atomic_container": true}}
		if err := p.store.Ingest(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	a := queueDocument(t, p, "doc", docTextA, "")
	for _, ref := range []string{"doc#section-not-a-number", "doc-other#section-1"} {
		r, err := p.store.GetRecord(ctx, ref)
		if err != nil || r.Status != "active" {
			t.Fatal("legacy match was too broad")
		}
	}
	old, err := p.store.GetRecord(ctx, "doc#section-1")
	if err != nil || old.Status != "archived" {
		t.Fatal("legacy evidence stayed active")
	}
	p.SetLearningCaller(func(context.Context, string, string) (string, error) {
		return `{"atoms":[{"kind":"style","topic":"writing","content":"Use short paragraphs","quote":"Use sections and short paragraphs","visibility":"personal"}]}`, nil
	})
	if err = p.ProcessLearning(ctx, 1); err != nil {
		t.Fatal(err)
	}
	queueDocument(t, p, "doc", docTextB, a.Revision)
	back := queueDocument(t, p, "doc", docTextA, "")
	if err = p.ProcessLearning(ctx, 5); err != nil {
		t.Fatal(err)
	}
	rows, err := p.store.db.Query(`SELECT metadata FROM memories WHERE status='active' AND json_extract(metadata,'$.source')='personal_learning'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var meta map[string]any
		if err = json.Unmarshal([]byte(raw), &meta); err != nil {
			t.Fatal(err)
		}
		ids := meta["source_ids"].([]any)
		if ids[0] != back.SourceIDs[0] {
			t.Fatal("active atom has wrong generation")
		}
		count++
	}
	if err = rows.Err(); err != nil || count != 1 {
		t.Fatalf("fresh generation did not learn: %d %v", count, err)
	}
}
