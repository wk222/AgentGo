package memory

import (
	"context"
	"strings"
	"testing"
)

func stateOf(t *testing.T, p *Pipeline, id string) string {
	t.Helper()
	rec, err := p.store.GetRecord(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := rec.Metadata["source_state"].(string)
	return s
}

func distilled(t *testing.T, p *Pipeline, sourceID, atomID, ref string) Record {
	t.Helper()
	src, err := p.store.GetRecord(context.Background(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	r := Record{ID: atomID, Scope: "global", Modality: "fact", Content: "Use short structured paragraphs", Status: "active", Importance: .7, SourceTrust: .35,
		Metadata: map[string]interface{}{"source": "personal_learning", "memory_kind": "style", "verification": "derived_unverified", "source_ids": []string{sourceID},
			"source_version": stableDerivedMemoryID("learnversion", "", src.Content), "source_quote": "Use sections and short paragraphs", "source_ref": ref, "origin_scope": "s"}}
	if err := p.Ingest(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	return r
}
func TestFlagDocumentSourceMarksEvidenceAndLessonsOnly(t *testing.T) {
	p, ctx := learningFixture(t)
	a := queueDocument(t, p, "docs/report.docx", docTextA, "")
	lesson := distilled(t, p, a.SourceIDs[0], "lesson-a", "docs/report.docx")
	similar := queueDocument(t, p, "docs/report.docx.bak", docTextA, "")
	similarLesson := distilled(t, p, similar.SourceIDs[0], "lesson-similar", "docs/report.docx.bak")
	if err := p.Ingest(ctx, Record{ID: "independent", Scope: "global", Modality: "fact", Content: "A fact confirmed by the user", Status: "active", IsCanonical: true}); err != nil {
		t.Fatal(err)
	}

	n, err := p.store.FlagDocumentSource(ctx, "docs/report.docx", SourceChanged)
	if err != nil || n != 2 {
		t.Fatalf("flagged %d, %v (want the evidence and its lesson)", n, err)
	}
	for _, id := range []string{a.SourceIDs[0], lesson.ID} {
		if stateOf(t, p, id) != SourceChanged {
			t.Errorf("%s not marked", id)
		}
	}
	for _, id := range []string{similar.SourceIDs[0], similarLesson.ID, "independent"} {
		if stateOf(t, p, id) != "" {
			t.Errorf("%s marked although it comes from another source", id)
		}
	}
	// Annotation only: still active, content untouched.
	rec, _ := p.store.GetRecord(ctx, lesson.ID)
	if rec.Status != "active" || rec.Content != lesson.Content {
		t.Fatalf("flagging altered the memory: %+v", rec)
	}
	if rec.Metadata["verification"] != "derived_unverified" || rec.Metadata["memory_kind"] != "style" {
		t.Fatalf("flagging lost existing metadata: %+v", rec.Metadata)
	}
}

func TestFlagDocumentSourceIsIdempotentAndUpgradesToDeleted(t *testing.T) {
	p, ctx := learningFixture(t)
	a := queueDocument(t, p, "r.docx", docTextA, "")
	if n, _ := p.store.FlagDocumentSource(ctx, "r.docx", SourceChanged); n != 1 {
		t.Fatalf("first flag = %d", n)
	}
	rev1, _ := p.store.TreeRevision(ctx, "s")
	if n, err := p.store.FlagDocumentSource(ctx, "r.docx", SourceChanged); err != nil || n != 0 {
		t.Fatalf("repeating the same state must change nothing: %d %v", n, err)
	}
	if rev2, _ := p.store.TreeRevision(ctx, "s"); rev1 != rev2 {
		t.Fatal("a no-op flag must not invalidate the summary tree")
	}
	if n, _ := p.store.FlagDocumentSource(ctx, "r.docx", SourceDeleted); n != 1 || stateOf(t, p, a.SourceIDs[0]) != SourceDeleted {
		t.Fatal("a deleted source must overwrite changed")
	}
}

func TestFlagDocumentSourceInvalidatesTheSummaryTreeRevision(t *testing.T) {
	p, ctx := learningFixture(t)
	a := queueDocument(t, p, "r.docx", docTextA, "")
	distilled(t, p, a.SourceIDs[0], "lesson", "r.docx")
	before, _ := p.store.TreeRevision(ctx, "global")
	ctxBefore, _ := p.ContextRevision(ctx, "s")
	if n, _ := p.store.FlagDocumentSource(ctx, "r.docx", SourceChanged); n != 2 {
		t.Fatal("setup")
	}
	if after, _ := p.store.TreeRevision(ctx, "global"); after == before {
		t.Fatal("the summary tree must see the flagged memory as changed and rebuild")
	}
	if ctxAfter, _ := p.ContextRevision(ctx, "s"); ctxAfter == ctxBefore {
		t.Fatal("cached context must be refreshed")
	}
}

func TestFlagDocumentSourceSectionsAndNumericSuffixOnly(t *testing.T) {
	p, ctx := learningFixture(t)
	long := strings.Repeat("Use clear headings and short paragraphs in every report. ", 600)
	multi, err := p.ReplaceDocumentLearning(ctx, "s", "big.docx", "writing", long, "")
	if err != nil || len(multi.SourceIDs) < 2 {
		t.Fatalf("setup needs a multi-section document: %+v %v", multi, err)
	}
	other := queueDocument(t, p, "big.docx#section-x", docTextB, "")
	n, err := p.store.FlagDocumentSource(ctx, "big.docx", SourceChanged)
	if err != nil || n != len(multi.SourceIDs) {
		t.Fatalf("flagged %d of %d sections: %v", n, len(multi.SourceIDs), err)
	}
	if stateOf(t, p, other.SourceIDs[0]) != "" {
		t.Fatal("a non-numeric suffix is another document")
	}
}

func TestFlagDocumentSourceRejectsBadInput(t *testing.T) {
	p, ctx := learningFixture(t)
	for _, c := range []struct{ src, state string }{{"", SourceChanged}, {"  ", SourceDeleted}, {"a", "gone"}, {"a", ""}} {
		if _, err := p.store.FlagDocumentSource(ctx, c.src, c.state); err == nil {
			t.Errorf("%+v must be rejected", c)
		}
	}
	if n, err := p.store.FlagDocumentSource(ctx, "nothing-learned.docx", SourceChanged); err != nil || n != 0 {
		t.Fatalf("an unknown source flags nothing: %d %v", n, err)
	}
}

func TestRecallLabelNamesAChangedOrDeletedSource(t *testing.T) {
	base := Record{ID: "m1", Modality: "journal", Content: "Use short paragraphs", Metadata: map[string]interface{}{"verification": "derived_unverified"}}
	plain := formatRecallRecord(base, 0)
	if strings.Contains(plain, "来源文件") {
		t.Fatalf("an unflagged record must read as before: %s", plain)
	}
	changed := base
	changed.Metadata = map[string]interface{}{"verification": "derived_unverified", "source_state": SourceChanged}
	if got := formatRecallRecord(changed, 0); !strings.Contains(got, "来源文件已变化") || !strings.Contains(got, "归纳，未核实") {
		t.Fatalf("changed: %s", got)
	}
	deleted := base
	deleted.Metadata = map[string]interface{}{"source_state": SourceDeleted}
	if got := formatRecallRecord(deleted, 0); !strings.Contains(got, "来源文件已删除") {
		t.Fatalf("deleted: %s", got)
	}
}
