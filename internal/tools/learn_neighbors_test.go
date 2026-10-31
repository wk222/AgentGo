package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentgo/internal/memory"
)

func metaInt(v any) int {
	if f, ok := v.(float64); ok {
		return int(f)
	}
	if n, ok := v.(int); ok {
		return n
	}
	return -1
}

type learnEnv struct {
	*indexEnv
	s     *memory.SQLiteStore
	r     *Registry
	scope string
}

func newLearnEnv(t *testing.T) *learnEnv {
	t.Helper()
	e := newIndexEnv(t)
	s, err := memory.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DB().Close() })
	le := &learnEnv{indexEnv: e, s: s, r: NewRegistry(), scope: "sess-a"}
	if err := RegisterDocumentLearningTools(le.r, memory.NewPipeline(s), func(context.Context) string { return le.scope }, func() string { return e.root }); err != nil {
		t.Fatal(err)
	}
	return le
}

func (le *learnEnv) learn(in learnDocumentInput) learnDocumentOutput {
	le.t.Helper()
	var out learnDocumentOutput
	if err := json.Unmarshal([]byte(invokeLearningTool(le.t, le.r, "learn_document", in)), &out); err != nil {
		le.t.Fatal(err)
	}
	return out
}

func (le *learnEnv) neighbors(id string, before, after int) learnedNeighborsOutput {
	le.t.Helper()
	var out learnedNeighborsOutput
	if err := json.Unmarshal([]byte(invokeLearningTool(le.t, le.r, "read_learned_neighbors", learnedNeighborsInput{SourceID: id, Before: before, After: after})), &out); err != nil {
		le.t.Fatal(err)
	}
	return out
}

// A budget workbook long enough to be learned in many sections; each section must say which rows it holds.
func (le *learnEnv) budgetWorkbook(rows int, marker string) {
	var data [][]any
	for i := 1; i <= rows; i++ {
		note := fmt.Sprintf("第%d项经费说明，按制度需要逐级审批并保留票据", i)
		if i == rows/2 {
			note = marker + "：单笔超过五万元须提交财务处负责人复核"
		}
		data = append(data, []any{fmt.Sprintf("费用%d", i), note})
	}
	le.writeXLSX("budget.xlsx", map[string][][]any{"预算": data})
}

func TestLearnByPathKeepsWhereEachSectionCameFromAndNeighboursAreReadable(t *testing.T) {
	le := newLearnEnv(t)
	le.budgetWorkbook(900, "ALPHA-CONDITION")
	got := le.learn(learnDocumentInput{Path: "budget.xlsx", Topic: "预算制度"})
	if got.Error != "" || !got.Queued || len(got.SourceIDs) < 4 {
		t.Fatalf("learn by path: %+v", got)
	}
	if len(got.Locators) != len(got.SourceIDs) {
		t.Fatalf("locators %v do not line up with %d sections", got.Locators, len(got.SourceIDs))
	}
	for i, l := range got.Locators {
		if !strings.Contains(l, `sheet "预算" rows`) {
			t.Fatalf("section %d locator = %q", i, l)
		}
	}
	// The section holding the condition: what a hit alone gives, versus what a neighbour read adds.
	condition := -1
	for i, id := range got.SourceIDs {
		rec, err := le.s.GetRecord(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rec.Content, "ALPHA-CONDITION") {
			condition = i
		}
		if rec.Metadata["document_locator"] != got.Locators[i] || metaInt(rec.Metadata["document_total"]) != len(got.SourceIDs) {
			t.Fatalf("section %d lost its location in storage: %v", i, rec.Metadata)
		}
	}
	if condition < 1 || condition >= len(got.SourceIDs)-1 {
		t.Fatalf("test document: condition in section %d of %d", condition, len(got.SourceIDs))
	}
	mid := le.neighbors(got.SourceIDs[condition], 1, 1)
	if mid.Error != "" || mid.Center.ID != got.SourceIDs[condition] || len(mid.Before) != 1 || len(mid.After) != 1 {
		t.Fatalf("neighbours: %+v", mid)
	}
	if mid.Before[0].ID != got.SourceIDs[condition-1] || mid.After[0].ID != got.SourceIDs[condition+1] {
		t.Fatalf("neighbours are not the adjacent sections: %+v", mid)
	}
	if mid.Before[0].Locator == mid.Center.Locator || mid.Before[0].Locator == "" {
		t.Fatalf("neighbour locators must name their own rows: %q vs %q", mid.Before[0].Locator, mid.Center.Locator)
	}
	// Bounds: two before the first section is the start of the document, not an error.
	first := le.neighbors(got.SourceIDs[0], 2, 1)
	if first.Error != "" || len(first.Before) != 0 || !first.AtStart || len(first.After) != 1 {
		t.Fatalf("start of document: %+v", first)
	}
	last := le.neighbors(got.SourceIDs[len(got.SourceIDs)-1], 1, 3)
	if last.Error != "" || len(last.After) != 0 || !last.AtEnd {
		t.Fatalf("end of document: %+v", last)
	}
	if over := le.neighbors(got.SourceIDs[0], 9, 0); over.Error == "" {
		t.Fatal("a request for more than 3 neighbours must be refused")
	}
}

func TestNeighbourReadStaysInsideSameVersionAndSameSession(t *testing.T) {
	le := newLearnEnv(t)
	le.budgetWorkbook(900, "ALPHA-CONDITION")
	v1 := le.learn(learnDocumentInput{Path: "budget.xlsx", Topic: "预算制度"})
	id := v1.SourceIDs[1]

	// Another session cannot read this session's sections.
	le.scope = "sess-b"
	if out := le.neighbors(id, 1, 1); out.Error == "" || len(out.After) != 0 {
		t.Fatalf("a foreign session read someone's learned sections: %+v", out)
	}
	le.scope = "sess-a"
	if out := le.neighbors(id, 1, 1); out.Error != "" {
		t.Fatalf("own session: %+v", out)
	}

	// The workbook changes and is learned again: the old section ids are of an older version.
	le.budgetWorkbook(900, "BETA-REVISED")
	v2 := le.learn(learnDocumentInput{Path: "budget.xlsx", Topic: "预算制度"})
	if v2.Revision == v1.Revision || !v2.Changed {
		t.Fatalf("changed content must be a new version: %+v", v2)
	}
	if out := le.neighbors(id, 1, 1); !strings.Contains(out.Error, "no such learned section") && !strings.Contains(out.Error, "source_changed") {
		t.Fatalf("an old version's section answered: %+v", out)
	}
	if out := le.neighbors(v2.SourceIDs[1], 1, 1); out.Error != "" || out.Center.ID != v2.SourceIDs[1] {
		t.Fatalf("new version: %+v", out)
	}

	// Withdrawn documents do not answer either.
	le.learn(learnDocumentInput{Source: "budget.xlsx", Operation: "delete"})
	if out := le.neighbors(v2.SourceIDs[1], 1, 1); out.Error == "" {
		t.Fatalf("a withdrawn document answered: %+v", out)
	}
}

func TestLearnByPathRefusesWhatItCannotRead(t *testing.T) {
	le := newLearnEnv(t)
	le.write("notes.txt", []byte("short note"))
	cases := map[string]learnDocumentInput{
		"both path and text": {Path: "notes.txt", Text: "also text", Topic: "t"},
		"outside workspace":  {Path: "../outside.txt", Topic: "t"},
		"missing file":       {Path: "nope.txt", Topic: "t"},
	}
	for name, in := range cases {
		if out := le.learn(in); out.Error == "" || out.Queued {
			t.Fatalf("%s accepted: %+v", name, out)
		}
	}
	// Plain text files work and simply carry no page or row locators.
	ok := le.learn(learnDocumentInput{Path: "notes.txt", Topic: "t"})
	if ok.Error != "" || !ok.Queued || len(ok.Locators) != 0 {
		t.Fatalf("plain file: %+v", ok)
	}
	// The same bytes by path and by text are the same version.
	again := le.learn(learnDocumentInput{Source: "notes.txt", Text: "short note", Topic: "t"})
	if again.Revision != ok.Revision || again.Changed {
		t.Fatalf("path and text of the same content must agree on the version: %+v vs %+v", again, ok)
	}
}
