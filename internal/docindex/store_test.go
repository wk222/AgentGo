package docindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(func() *sql.DB { return db }), db
}

func file(source, revision string) File {
	return File{Scope: "ws", Source: source, SHA256: "h-" + revision, ParserVersion: "v1", Revision: revision, Size: 10, ModTime: 20, TotalChars: 100}
}

func chunk(ord int, text string) Chunk {
	return Chunk{ID: fmt.Sprintf("c%d-%s", ord, text), Ord: ord, Start: ord * 100, End: ord*100 + len([]rune(text)), Text: text, Spans: json.RawMessage(`[]`)}
}

func ftsRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM doc_index_fts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sources(hits []Hit) string {
	var s []string
	for _, h := range hits {
		s = append(s, h.Source)
	}
	return strings.Join(s, ",")
}

func TestChineseQuestionFindsChunkWithoutSharedWordBoundaries(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	must(t, st.Replace(ctx, file("policy.txt", "r1"), []Chunk{
		chunk(0, "出差报销单必须在返程后三十日内提交。"),
		chunk(1, "办公用品采购需要部门负责人审批。"),
	}))
	hits, err := st.Search(ctx, "ws", "出差报销怎么走", SearchOptions{})
	must(t, err)
	if len(hits) == 0 || !strings.Contains(hits[0].Text, "出差报销单") {
		t.Fatalf("hits=%+v", hits)
	}
	if hits[0].Source != "policy.txt" || hits[0].Revision != "r1" || hits[0].Start != 0 || hits[0].End != 18 {
		t.Fatalf("hit lost its locator: %+v", hits[0])
	}
}

func TestMixedLatinAndHanTerms(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	must(t, st.Replace(ctx, file("a.txt", "r1"), []Chunk{chunk(0, "Q3 revenue grew 12% 营业收入同比增长")}))
	for _, q := range []string{"revenue", "REVENUE", "营业收入", "reven"} {
		hits, err := st.Search(ctx, "ws", q, SearchOptions{})
		must(t, err)
		if len(hits) != 1 {
			t.Errorf("query %q: hits=%d", q, len(hits))
		}
	}
}

func TestQueryCannotInjectFTSSyntax(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	must(t, st.Replace(ctx, file("a.txt", "r1"), []Chunk{chunk(0, "alpha beta gamma")}))
	for _, q := range []string{`alpha" OR "`, `NEAR(alpha beta)`, `alpha AND`, `*`, `"`, `a:b`, `-alpha`, `(`, ``, `   `, `的`} {
		if _, err := st.Search(ctx, "ws", q, SearchOptions{}); err != nil {
			t.Errorf("query %q must not error: %v", q, err)
		}
	}
	hits, _ := st.Search(ctx, "ws", `alpha" OR "zzz`, SearchOptions{})
	if len(hits) != 1 {
		t.Errorf("quoted query should still find alpha, got %d", len(hits))
	}
}

func TestReplaceSwapsContentAndLeavesNoOrphanTerms(t *testing.T) {
	st, db := openTestStore(t)
	ctx := context.Background()
	must(t, st.Replace(ctx, file("a.txt", "r1"), []Chunk{chunk(0, "old budget figures"), chunk(1, "old travel rules")}))
	must(t, st.Replace(ctx, file("a.txt", "r2"), []Chunk{chunk(0, "new hiring plan")}))
	if n := ftsRows(t, db); n != 1 {
		t.Fatalf("fts rows after replace = %d, want 1 (old terms must be gone)", n)
	}
	if hits, _ := st.Search(ctx, "ws", "budget", SearchOptions{}); len(hits) != 0 {
		t.Fatalf("old content still searchable: %+v", hits)
	}
	hits, _ := st.Search(ctx, "ws", "hiring", SearchOptions{})
	if len(hits) != 1 || hits[0].Revision != "r2" {
		t.Fatalf("hits=%+v", hits)
	}
	f, ok, err := st.Get(ctx, "ws", "a.txt")
	if err != nil || !ok || f.Revision != "r2" || f.ChunkCount != 1 {
		t.Fatalf("file row = %+v ok=%v err=%v", f, ok, err)
	}
}

func TestFailedReplaceKeepsOldContent(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	must(t, st.Replace(ctx, file("a.txt", "r1"), []Chunk{chunk(0, "stable content")}))
	// A duplicate chunk insert cannot fail, so force a failure with a missing
	// revision, which is rejected before anything is touched, and with a bad
	// context, which fails mid-way.
	bad := file("a.txt", "")
	if err := st.Replace(ctx, bad, []Chunk{chunk(0, "new")}); err == nil {
		t.Fatal("empty revision must be rejected")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := st.Replace(cancelled, file("a.txt", "r2"), []Chunk{chunk(0, "new")}); err == nil {
		t.Fatal("cancelled context must fail")
	}
	hits, _ := st.Search(ctx, "ws", "stable", SearchOptions{})
	if len(hits) != 1 || hits[0].Revision != "r1" {
		t.Fatalf("old content lost after failed replace: %+v", hits)
	}
}

func TestRemoveForgetsSourceAndReportsAbsent(t *testing.T) {
	st, db := openTestStore(t)
	ctx := context.Background()
	must(t, st.Replace(ctx, file("a.txt", "r1"), []Chunk{chunk(0, "keep me alpha")}))
	must(t, st.Replace(ctx, file("b.txt", "r1"), []Chunk{chunk(0, "remove me alpha")}))
	ok, err := st.Remove(ctx, "ws", "b.txt")
	if err != nil || !ok {
		t.Fatalf("remove = %v, %v", ok, err)
	}
	hits, _ := st.Search(ctx, "ws", "alpha", SearchOptions{})
	if sources(hits) != "a.txt" {
		t.Fatalf("hits after remove: %s", sources(hits))
	}
	if n := ftsRows(t, db); n != 1 {
		t.Fatalf("fts rows = %d", n)
	}
	if ok, _ := st.Remove(ctx, "ws", "b.txt"); ok {
		t.Fatal("second remove must report not indexed")
	}
}

func TestScopesAreIsolated(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	other := file("a.txt", "r1")
	other.Scope = "other"
	must(t, st.Replace(ctx, file("a.txt", "r1"), []Chunk{chunk(0, "shared word alpha")}))
	must(t, st.Replace(ctx, other, []Chunk{chunk(0, "shared word alpha")}))
	hits, _ := st.Search(ctx, "ws", "alpha", SearchOptions{})
	if len(hits) != 1 {
		t.Fatalf("scope leak: %d hits", len(hits))
	}
	if ok, _ := st.Remove(ctx, "ws", "a.txt"); !ok {
		t.Fatal("remove")
	}
	if hits, _ := st.Search(ctx, "other", "alpha", SearchOptions{}); len(hits) != 1 {
		t.Fatal("removing from one scope must not touch another")
	}
}

func TestPrefixAndExcludeFilters(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	for _, s := range []string{"docs/a.txt", "docs/sub/b.txt", "docs2/c.txt", "d_s/x.txt", "dxs/y.txt", "root.txt"} {
		must(t, st.Replace(ctx, file(s, "r1"), []Chunk{chunk(0, "common alpha text")}))
	}
	get := func(opt SearchOptions) string {
		hits, err := st.Search(ctx, "ws", "alpha", opt)
		must(t, err)
		var s []string
		for _, h := range hits {
			s = append(s, h.Source)
		}
		sortStrings(s)
		return strings.Join(s, ",")
	}
	if got := get(SearchOptions{Prefix: "docs"}); got != "docs/a.txt,docs/sub/b.txt" {
		t.Errorf("dir prefix = %s (docs2 must not match docs)", got)
	}
	if got := get(SearchOptions{Prefix: "docs/"}); got != "docs/a.txt,docs/sub/b.txt" {
		t.Errorf("trailing slash prefix = %s", got)
	}
	if got := get(SearchOptions{Prefix: "root.txt"}); got != "root.txt" {
		t.Errorf("file prefix = %s", got)
	}
	if got := get(SearchOptions{Prefix: "d_s"}); got != "d_s/x.txt" {
		t.Errorf("underscore must not act as a wildcard: %s", got)
	}
	if got := get(SearchOptions{Exclude: []string{"docs/a.txt", "root.txt"}}); strings.Contains(got, "docs/a.txt") || strings.Contains(got, "root.txt") || !strings.Contains(got, "docs2/c.txt") {
		t.Errorf("exclude = %s", got)
	}
}

func TestRankingPrefersChunkMatchingMoreTerms(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	must(t, st.Replace(ctx, file("a.txt", "r1"), []Chunk{
		chunk(0, "invoice only here"),
		chunk(1, "invoice approval workflow for vendors"),
	}))
	hits, _ := st.Search(ctx, "ws", "vendor invoice approval", SearchOptions{})
	if len(hits) != 2 || hits[0].Ord != 1 {
		t.Fatalf("hits=%+v", hits)
	}
	if hits[0].Score > hits[1].Score {
		t.Fatalf("scores must be ascending (bm25): %v %v", hits[0].Score, hits[1].Score)
	}
}

func TestTouchUpdatesStatOnly(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	must(t, st.Replace(ctx, file("a.txt", "r1"), []Chunk{chunk(0, "alpha")}))
	must(t, st.Touch(ctx, "ws", "a.txt", 99, 77))
	f, _, _ := st.Get(ctx, "ws", "a.txt")
	if f.Size != 99 || f.ModTime != 77 || f.Revision != "r1" || f.ChunkCount != 1 {
		t.Fatalf("%+v", f)
	}
}

func TestStoreWithoutDatabaseReportsUnavailable(t *testing.T) {
	st := New(func() *sql.DB { return nil })
	if _, err := st.Search(context.Background(), "ws", "x", SearchOptions{}); err != ErrUnavailable {
		t.Fatalf("err = %v", err)
	}
	if _, err := st.List(context.Background(), "ws", ""); err != ErrUnavailable {
		t.Fatalf("err = %v", err)
	}
}

func TestStoreOpensWhenDatabaseAppearsLater(t *testing.T) {
	var db *sql.DB
	st := New(func() *sql.DB { return db })
	if _, err := st.List(context.Background(), "ws", ""); err != ErrUnavailable {
		t.Fatalf("err = %v", err)
	}
	real, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "late.db"))
	must(t, err)
	t.Cleanup(func() { _ = real.Close() })
	db = real
	if files, err := st.List(context.Background(), "ws", ""); err != nil || len(files) != 0 {
		t.Fatalf("files=%v err=%v", files, err)
	}
}

func TestPiecesAndMatchQuery(t *testing.T) {
	cases := []struct{ in, terms, match string }{
		{"出差报销", "出差 差报 报销", `"出差" OR "差报" OR "报销"`},
		{"Q3 报告", "q3 报告", `"q3" OR "报告"`},
		{"表", "表", `"表"`},
		{"a b", "a b", ``},
		{"invoice", "invoice", `"invoice"*`},
		{"的", "的", ``},
	}
	for _, c := range cases {
		if got := Terms(c.in); got != c.terms {
			t.Errorf("Terms(%q) = %q, want %q", c.in, got, c.terms)
		}
		if got := MatchQuery(c.in); got != c.match {
			t.Errorf("MatchQuery(%q) = %q, want %q", c.in, got, c.match)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestAroundReturnsNeighboursOfTheSameRevisionOnly(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	var cs []Chunk
	for i := 0; i < 5; i++ {
		cs = append(cs, chunk(i, fmt.Sprintf("part %d", i)))
	}
	must(t, st.Replace(ctx, file("a.txt", "r1"), cs))
	other := file("b.txt", "r1")
	must(t, st.Replace(ctx, other, []Chunk{chunk(0, "other doc part 0"), chunk(1, "other doc part 1")}))

	got, center, err := st.Around(ctx, "ws", "a.txt", "r1", 250, 1, 1) // chunk 2 spans [200,206)... offset inside chunk 2? 250 is a gap
	must(t, err)
	if got != nil {
		t.Fatalf("an offset in no chunk must find nothing: %+v", got)
	}
	got, center, err = st.Around(ctx, "ws", "a.txt", "r1", 202, 1, 1)
	must(t, err)
	if len(got) != 3 || got[center].Ord != 2 || got[0].Ord != 1 || got[2].Ord != 3 {
		t.Fatalf("around = %+v center=%d", got, center)
	}
	for _, h := range got {
		if h.Source != "a.txt" {
			t.Fatalf("neighbour from another document: %+v", h)
		}
	}
	// Edges return what exists.
	got, center, _ = st.Around(ctx, "ws", "a.txt", "r1", 0, 3, 1)
	if len(got) != 2 || center != 0 || got[1].Ord != 1 {
		t.Fatalf("start of document: %+v center=%d", got, center)
	}
	got, center, _ = st.Around(ctx, "ws", "a.txt", "r1", 402, 1, 5)
	if len(got) != 2 || center != 1 {
		t.Fatalf("end of document: %+v center=%d", got, center)
	}
	// Another version of the document yields nothing, never a mixture.
	if got, _, _ := st.Around(ctx, "ws", "a.txt", "r0", 202, 1, 1); got != nil {
		t.Fatalf("wrong revision: %+v", got)
	}
	must(t, st.Replace(ctx, file("a.txt", "r2"), []Chunk{chunk(0, "new part 0"), chunk(1, "new part 1")}))
	if got, _, _ := st.Around(ctx, "ws", "a.txt", "r1", 102, 1, 1); got != nil {
		t.Fatalf("a replaced revision must not be readable: %+v", got)
	}
	if _, _, err := st.Around(ctx, "ws", "a.txt", "r2", 0, -1, 0); err == nil {
		t.Fatal("negative counts must be rejected")
	}
	if got, _, _ := st.Around(ctx, "other", "a.txt", "r2", 0, 1, 1); got != nil {
		t.Fatal("scope leak")
	}
}

// Chunkers that overlap their chunks make one offset fall inside two; the chunk
// that starts at the offset is the one the caller means.
func TestAroundPrefersTheChunkStartingAtTheOffset(t *testing.T) {
	st, _ := openTestStore(t)
	ctx := context.Background()
	cs := []Chunk{
		{ID: "a", Ord: 0, Start: 0, End: 100, Text: strings.Repeat("a", 100)},
		{ID: "b", Ord: 1, Start: 80, End: 180, Text: strings.Repeat("b", 100)},
		{ID: "c", Ord: 2, Start: 160, End: 260, Text: strings.Repeat("c", 100)},
	}
	must(t, st.Replace(ctx, file("a.txt", "r1"), cs))
	got, center, err := st.Around(ctx, "ws", "a.txt", "r1", 80, 1, 1)
	must(t, err)
	if len(got) != 3 || got[center].Ord != 1 {
		t.Fatalf("centre = %+v", got[center])
	}
	// An offset that starts no chunk falls back to the first that contains it.
	got, center, _ = st.Around(ctx, "ws", "a.txt", "r1", 90, 0, 0)
	if len(got) != 1 || got[center].Ord != 0 {
		t.Fatalf("fallback = %+v", got)
	}
}
