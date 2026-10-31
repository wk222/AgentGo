package memory

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A learned lesson keeps where its evidence section sits, so it can be cited and the sections around it read.
func TestLearnedAtomCarriesLocatorAndItsEvidenceHasNeighbours(t *testing.T) {
	p, ctx := learningFixture(t)
	sections := []DocumentSection{
		{Text: "Use sections and short paragraphs. Cite original sources clearly.", Locator: "page 1", Start: 0, End: 66},
		{Text: "Group content by subject and retain dates in headings.", Locator: "page 2", Start: 66, End: 120},
		{Text: "Close with the decision and the owner of the next step.", Locator: "page 3", Start: 120, End: 175},
	}
	res, err := p.ReplaceDocumentSections(ctx, "s", "memo.pdf", "writing", sections, "")
	if err != nil || len(res.SourceIDs) != 3 {
		t.Fatal(res, err)
	}
	p.SetLearningCaller(func(context.Context, string, string) (string, error) {
		return `{"atoms":[{"kind":"style","topic":"writing","content":"Use short paragraphs","quote":"Use sections and short paragraphs","visibility":"personal"}]}`, nil
	})
	if err = p.ProcessLearning(ctx, 5); err != nil {
		t.Fatal(err)
	}
	recs, err := p.store.db.QueryContext(ctx, `SELECT `+selectCols+` FROM memories m WHERE m.status='active' AND json_extract(m.metadata,'$.source')='personal_learning'`)
	if err != nil {
		t.Fatal(err)
	}
	var atoms []Record
	for recs.Next() {
		r, e := p.store.scanRecord(recs)
		if e != nil {
			t.Fatal(e)
		}
		atoms = append(atoms, r)
	}
	recs.Close()
	if len(atoms) == 0 {
		t.Fatal("no atom was learned")
	}
	a := atoms[0]
	if a.Metadata["document_locator"] != "page 1" || metaInt(a.Metadata["document_section"]) != 0 || metaInt(a.Metadata["document_total"]) != 3 || a.Metadata["document_source"] != "memo.pdf" {
		t.Fatalf("the atom lost where its evidence sits: %v", a.Metadata)
	}
	ids, _ := a.Metadata["source_ids"].([]string)
	if len(ids) == 0 {
		if raw, ok := a.Metadata["source_ids"].([]any); ok && len(raw) > 0 {
			ids = []string{raw[0].(string)}
		}
	}
	n, err := p.DocumentNeighbors(ctx, "s", ids[0], 0, 2)
	if err != nil || len(n.After) != 2 || n.After[1].Locator != "page 3" || !strings.Contains(n.After[1].Content, "owner of the next step") {
		t.Fatalf("neighbours of the atom's evidence: %+v %v", n, err)
	}
	// What the hit alone holds does not include the conclusion on page 3.
	if strings.Contains(n.Center.Content, "owner of the next step") {
		t.Fatal("the centre section must be only its own text")
	}
}

func TestDocumentNeighborsRefusesStaleForeignAndWithdrawnSections(t *testing.T) {
	p, ctx := learningFixture(t)
	v1, err := p.ReplaceDocumentSections(ctx, "s", "doc", "t", []DocumentSection{{Text: "one"}, {Text: "two"}, {Text: "three"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.DocumentNeighbors(ctx, "other", v1.SourceIDs[1], 1, 1); !errors.Is(err, ErrLearnedSectionGone) {
		t.Fatalf("a foreign scope read the sections: %v", err)
	}
	if _, err = p.DocumentNeighbors(ctx, "s", "no-such-id", 1, 1); !errors.Is(err, ErrLearnedSectionGone) {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err = p.DocumentNeighbors(ctx, "s", v1.SourceIDs[1], 4, 0); err == nil {
		t.Fatal("more than 3 neighbours must be refused")
	}
	if _, err = p.ReplaceDocumentSections(ctx, "s", "doc", "t", []DocumentSection{{Text: "uno"}, {Text: "dos"}}, v1.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = p.DocumentNeighbors(ctx, "s", v1.SourceIDs[1], 1, 1); err == nil {
		t.Fatal("an older version's section answered")
	}
}
