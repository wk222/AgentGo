package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// LearnedSection is one stored piece of a learned document, as read back.
type LearnedSection struct {
	ID      string `json:"id"`
	Section int    `json:"section"` // zero-based position in the document
	Total   int    `json:"total_sections,omitempty"`
	Locator string `json:"locator,omitempty"` // "page 3", `sheet "Budget" rows 2-40`
	Heading string `json:"heading,omitempty"`
	Content string `json:"content"`
	// SourceState is "changed" or "deleted" when the file this was learned from is no longer what it was.
	SourceState string `json:"source_state,omitempty"`
}

// LearnedNeighbors is a piece of a learned document with the pieces around it.
type LearnedNeighbors struct {
	Source   string           `json:"source"`
	Revision string           `json:"revision"`
	Before   []LearnedSection `json:"before"`
	Center   LearnedSection   `json:"center"`
	After    []LearnedSection `json:"after"`
	AtStart  bool             `json:"at_start_of_document,omitempty"`
	AtEnd    bool             `json:"at_end_of_document,omitempty"`
}

// ErrLearnedSectionGone means the evidence section is not available to this reader: unknown, withdrawn,
// from another scope, or from a version of the document that was replaced since.
var ErrLearnedSectionGone = errors.New("learned section not found")

// ErrLearnedSourceChanged means the document was learned again after the caller saw this section.
var ErrLearnedSourceChanged = errors.New("source_changed: this section belongs to an older version of the document")

const learnedNeighborMax = 3

// DocumentNeighbors reads the sections just before and after one learned evidence section (the id a learned
// memory lists in source_ids). The neighbours always come from the same document, the same version of it
// (generation) and the same scope as the section asked about: a replaced, withdrawn or foreign section
// answers with an error, never with someone else's text.
func (p *Pipeline) DocumentNeighbors(ctx context.Context, scope, evidenceID string, before, after int) (LearnedNeighbors, error) {
	var out LearnedNeighbors
	if p == nil || p.store == nil {
		return out, errors.New("learning store unavailable")
	}
	scope, evidenceID = strings.TrimSpace(scope), strings.TrimSpace(evidenceID)
	if scope == "" || evidenceID == "" {
		return out, errors.New("scope and evidence id required")
	}
	if before < 0 || after < 0 || before > learnedNeighborMax || after > learnedNeighborMax {
		return out, fmt.Errorf("before and after must be between 0 and %d", learnedNeighborMax)
	}
	if before == 0 && after == 0 {
		before, after = 1, 1
	}
	rec, err := p.store.GetRecord(ctx, evidenceID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrLearnedSectionGone
	}
	if err != nil {
		return out, err
	}
	if rec.Status != "active" || rec.Metadata["source"] != "personal_learning_evidence" || rec.Metadata["source_kind"] != "document" ||
		rec.Metadata["origin_scope"] != scope {
		return out, ErrLearnedSectionGone
	}
	source, _ := rec.Metadata["document_source"].(string)
	generation, _ := rec.Metadata["document_generation"].(string)
	if source == "" || generation == "" {
		return out, ErrLearnedSectionGone
	}
	// The document must still be on this generation: replaced or withdrawn documents have a new one or none.
	var curGen, state, revision string
	err = p.store.db.QueryRowContext(ctx, `SELECT generation,state,revision FROM memory_documents WHERE scope=? AND source=?`, scope, source).Scan(&curGen, &state, &revision)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (state != "active" || curGen != generation)) {
		return out, ErrLearnedSourceChanged
	}
	if err != nil {
		return out, err
	}
	centerOrd := metaInt(rec.Metadata["document_section"])
	rows, err := p.store.db.QueryContext(ctx, `SELECT `+selectCols+` FROM memories m WHERE m.status='active'
 AND json_extract(m.metadata,'$.source')='personal_learning_evidence' AND json_extract(m.metadata,'$.origin_scope')=?
 AND json_extract(m.metadata,'$.document_source')=? AND json_extract(m.metadata,'$.document_generation')=?
 AND CAST(json_extract(m.metadata,'$.document_section') AS INTEGER) BETWEEN ? AND ?
 ORDER BY CAST(json_extract(m.metadata,'$.document_section') AS INTEGER)`, scope, source, generation, centerOrd-before, centerOrd+after)
	if err != nil {
		return out, err
	}
	var recs []Record
	for rows.Next() {
		r, e := p.store.scanRecord(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		recs = append(recs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	out.Source, out.Revision = source, revision
	out.Before, out.After = []LearnedSection{}, []LearnedSection{}
	found := false
	for _, r := range recs {
		s := learnedSection(r)
		switch {
		case r.ID == evidenceID:
			out.Center, found = s, true
		case s.Section < centerOrd:
			out.Before = append(out.Before, s)
		default:
			out.After = append(out.After, s)
		}
	}
	if !found {
		return out, ErrLearnedSectionGone
	}
	out.AtStart = centerOrd-before < 0 || len(out.Before) < before
	out.AtEnd = len(out.After) < after
	return out, nil
}

func learnedSection(r Record) LearnedSection {
	s := LearnedSection{ID: r.ID, Section: metaInt(r.Metadata["document_section"]), Total: metaInt(r.Metadata["document_total"]), Content: r.Content}
	s.Locator, _ = r.Metadata["document_locator"].(string)
	s.Heading, _ = r.Metadata["document_heading"].(string)
	if st, _ := r.Metadata["source_state"].(string); st == SourceChanged || st == SourceDeleted {
		s.SourceState = st
	}
	return s
}

func metaInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
