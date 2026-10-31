package tools

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appdb "agentgo/internal/db"
	"agentgo/internal/docindex"
	"agentgo/internal/governance"
	"agentgo/internal/memory"

	"github.com/xuri/excelize/v2"
	_ "modernc.org/sqlite"
)

type indexEnv struct {
	t    *testing.T
	root string
	reg  *Registry
	d    *docIndexer
	db   *sql.DB
	ctx  context.Context
}

func newIndexEnv(t *testing.T) *indexEnv {
	t.Helper()
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// The same settings the application database gets: WAL, one connection.
	if err := appdb.Configure(db); err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	e := &indexEnv{t: t, root: root, reg: reg, db: db}
	e.d = &docIndexer{reg: reg, store: docindex.New(func() *sql.DB { return db }), root: func() string { return e.root }}
	e.ctx = WithEvidenceRun(governance.WithSessionID(context.Background(), "s1"))
	return e
}

func (e *indexEnv) write(rel string, data []byte) {
	e.t.Helper()
	p := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *indexEnv) writeXLSX(rel string, sheets map[string][][]any) {
	e.t.Helper()
	f := excelize.NewFile()
	first := true
	for name, rows := range sheets {
		if first {
			_ = f.SetSheetName("Sheet1", name)
			first = false
		} else {
			_, _ = f.NewSheet(name)
		}
		for r, row := range rows {
			for c, v := range row {
				cell, _ := excelize.CoordinatesToCellName(c+1, r+1)
				_ = f.SetCellValue(name, cell, v)
			}
		}
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		e.t.Fatal(err)
	}
	e.write(rel, buf.Bytes())
}

func (e *indexEnv) add(paths ...string) indexDocumentsOutput {
	e.t.Helper()
	out, err := e.d.index(e.ctx, indexDocumentsInput{Action: "add", Paths: paths})
	if err != nil {
		e.t.Fatalf("add %v: %v", paths, err)
	}
	return out
}

func (e *indexEnv) refresh(paths ...string) indexDocumentsOutput {
	e.t.Helper()
	out, err := e.d.index(e.ctx, indexDocumentsInput{Action: "refresh", Paths: paths})
	if err != nil {
		e.t.Fatalf("refresh: %v", err)
	}
	return out
}

func (e *indexEnv) search(q string) searchDocumentsOutput {
	e.t.Helper()
	out, err := e.d.search(e.ctx, searchDocumentsInput{Query: q})
	if err != nil {
		e.t.Fatalf("search %q: %v", q, err)
	}
	return out
}

func statusOf(out indexDocumentsOutput, path string) string {
	for _, f := range out.Files {
		if f.Path == path {
			return f.Status
		}
	}
	return ""
}

func hitPaths(out searchDocumentsOutput) string {
	var p []string
	for _, h := range out.Hits {
		p = append(p, h.Path)
	}
	return strings.Join(p, ",")
}

// The closed loop the index exists for: a PDF and a workbook are indexed, a
// question finds the right passage, and the citation on the hit resolves to the
// very text parse_file returns for the same range, with the page or the sheet
// and row it came from.
func TestPDFAndXLSXClosedLoop(t *testing.T) {
	e := newIndexEnv(t)
	e.write("contract.pdf", buildTestPDF([]string{
		"Page one covers definitions and parties only.",
		"The supplier grants a twelve month warranty clause covering parts and labour.",
		"Page three lists signatures.",
	}))
	e.writeXLSX("ledger.xlsx", map[string][][]any{"Sales": {
		{"customer", "item", "amount"},
		{"Alice", "widget", 120},
		{"Bob", "gadget", 75},
		{"Carol", "refund for damaged gizmo", 4200},
	}})
	out := e.add("contract.pdf", "ledger.xlsx")
	if out.Indexed != 2 || out.Failed != 0 {
		t.Fatalf("add: %+v", out)
	}

	pdf := e.search("warranty clause")
	if len(pdf.Hits) == 0 || pdf.Hits[0].Path != "contract.pdf" {
		t.Fatalf("pdf search: %+v", pdf)
	}
	h := pdf.Hits[0]
	if !strings.Contains(h.Text, "twelve month warranty") {
		t.Fatalf("hit text = %q", h.Text)
	}
	if !strings.Contains(h.Locator, "page 2") || strings.Contains(h.Locator, "page 1") {
		t.Fatalf("locator = %q, want the page the text is on", h.Locator)
	}
	assertCitationMatchesParseFile(t, e, h)

	xl := e.search("refund gizmo")
	if len(xl.Hits) == 0 || xl.Hits[0].Path != "ledger.xlsx" {
		t.Fatalf("xlsx search: %+v", xl)
	}
	xh := xl.Hits[0]
	if !strings.Contains(xh.Locator, `sheet "Sales"`) || !strings.Contains(xh.Locator, "4") {
		t.Fatalf("locator = %q", xh.Locator)
	}
	if !strings.Contains(xh.Text, "4200") {
		t.Fatalf("hit text = %q", xh.Text)
	}
	assertCitationMatchesParseFile(t, e, xh)
	// The row in the locator is the row of the cell in the workbook.
	spans := xh.Citation.Spans
	found := false
	for _, sp := range spans {
		if sp.Sheet == "Sales" && sp.RowStart <= 4 && sp.RowEnd >= 4 {
			found = true
		}
	}
	if !found {
		t.Fatalf("citation spans do not include Sales row 4: %+v", spans)
	}
}

// assertCitationMatchesParseFile proves a search hit is as good as a parse_file
// excerpt: same revision, same text for the same range, same locators, and the
// citation can be read back through read_evidence.
func assertCitationMatchesParseFile(t *testing.T, e *indexEnv, h docHit) {
	t.Helper()
	if h.Citation == nil || !strings.HasPrefix(h.Citation.ID, "ev_") {
		t.Fatalf("hit has no citation: %+v", h)
	}
	rec, err := readEvidence(e.ctx, h.Citation.ID)
	if err != nil || rec.Text != h.Text {
		t.Fatalf("read_evidence = %q, %v", rec.Text, err)
	}
	pf, err := readParsedFile(e.ctx, e.root, parseFileInput{
		RelativePath: h.Path, Offset: h.Citation.Start, Limit: h.Citation.End - h.Citation.Start, ExpectedRevision: h.Citation.Revision,
	})
	if err != nil {
		t.Fatalf("parse_file at the cited range: %v", err)
	}
	if pf.Text != h.Text {
		t.Fatalf("parse_file text %q != hit text %q", pf.Text, h.Text)
	}
	if pf.Source.Revision != h.Citation.Revision {
		t.Fatalf("revision differs: parse_file %s, hit %s", pf.Source.Revision, h.Citation.Revision)
	}
	if pf.Citation == nil || pf.Citation.ID != h.Citation.ID {
		t.Fatalf("the same range must give the same citation id: parse_file %+v, hit %s", pf.Citation, h.Citation.ID)
	}
	if fmt.Sprint(pf.Spans) != fmt.Sprint(h.Citation.Spans) {
		t.Fatalf("locators differ: parse_file %+v, hit %+v", pf.Spans, h.Citation.Spans)
	}
	if h.ReadMore == nil || h.ReadMore.Offset != h.Citation.Start || h.ReadMore.ExpectedRevision != h.Citation.Revision || h.ReadMore.RelativePath != h.Path {
		t.Fatalf("read_more = %+v", h.ReadMore)
	}
}

func TestChineseTextHitCitesExactRuneRange(t *testing.T) {
	e := newIndexEnv(t)
	e.write("制度/报销.txt", []byte("总则\n\n出差报销单必须在返程后三十日内提交财务部审核。\n\n办公用品采购需要部门负责人审批。\n"))
	e.add("制度")
	out := e.search("出差报销怎么走")
	if len(out.Hits) == 0 || !strings.Contains(out.Hits[0].Text, "返程后三十日") {
		t.Fatalf("%+v", out)
	}
	assertCitationMatchesParseFile(t, e, out.Hits[0])
}

func TestChangedFileIsLeftOutUntilRefreshed(t *testing.T) {
	e := newIndexEnv(t)
	e.write("notes.txt", []byte("the launch date is in March"))
	e.add("notes.txt")
	before := e.search("launch date")
	if len(before.Hits) != 1 {
		t.Fatalf("%+v", before)
	}
	oldRev := before.Hits[0].Citation.Revision

	e.write("notes.txt", []byte("the launch date moved to September"))
	stale := e.search("launch date")
	if len(stale.Hits) != 0 {
		t.Fatalf("a changed file must not be answered from the old index: %+v", stale.Hits)
	}
	if len(stale.Stale) != 1 || stale.Stale[0].Path != "notes.txt" || stale.Stale[0].State != "changed" || !strings.Contains(stale.Message, "refresh") {
		t.Fatalf("stale not reported: %+v", stale)
	}

	r := e.refresh()
	if r.Updated != 1 || statusOf(r, "notes.txt") != "updated" {
		t.Fatalf("refresh: %+v", r)
	}
	after := e.search("launch date")
	if len(after.Hits) != 1 || !strings.Contains(after.Hits[0].Text, "September") || len(after.Stale) != 0 {
		t.Fatalf("%+v", after)
	}
	if after.Hits[0].Citation.Revision == oldRev {
		t.Fatal("a changed file must get a new revision")
	}
	if old := e.search("March"); len(old.Hits) != 0 {
		t.Fatalf("old text still searchable after refresh: %+v", old.Hits)
	}
}

func TestDeletedFileInvalidatesAndRefreshDropsIt(t *testing.T) {
	e := newIndexEnv(t)
	e.write("keep.txt", []byte("shared topic alpha in keep"))
	e.write("gone.txt", []byte("shared topic alpha in gone"))
	e.add("keep.txt", "gone.txt")
	if got := hitPaths(e.search("alpha")); !strings.Contains(got, "gone.txt") {
		t.Fatalf("setup: %s", got)
	}
	if err := os.Remove(filepath.Join(e.root, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	out := e.search("alpha")
	if hitPaths(out) != "keep.txt" {
		t.Fatalf("deleted file still answers: %s", hitPaths(out))
	}
	if len(out.Stale) != 1 || out.Stale[0].State != "missing" {
		t.Fatalf("stale = %+v", out.Stale)
	}
	list, err := e.d.list(e.ctx, listIndexedInput{})
	if err != nil || list.Total != 2 || list.Outdated != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	r := e.refresh()
	if r.Removed != 1 || statusOf(r, "gone.txt") != "removed" {
		t.Fatalf("refresh: %+v", r)
	}
	list, _ = e.d.list(e.ctx, listIndexedInput{})
	if list.Total != 1 || list.Outdated != 0 || list.Documents[0].Path != "keep.txt" {
		t.Fatalf("list after refresh = %+v", list)
	}
	if out := e.search("alpha"); len(out.Stale) != 0 || hitPaths(out) != "keep.txt" {
		t.Fatalf("%+v", out)
	}
}

func TestTouchedButUnchangedFileStaysSearchable(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a.txt", []byte("budget approval steps"))
	e.add("a.txt")
	later := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(filepath.Join(e.root, "a.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	out := e.search("budget approval")
	if len(out.Hits) != 1 || len(out.Stale) != 0 {
		t.Fatalf("a file with the same content is current whatever its timestamp: %+v", out)
	}
	if r := e.add("a.txt"); r.Unchanged != 1 || r.Indexed+r.Updated != 0 {
		t.Fatalf("re-add: %+v", r)
	}
	// Touch recorded the new time, so the next check no longer needs the hash.
	f, _, _ := e.d.store.Get(e.ctx, e.scope(), "a.txt")
	if f.ModTime != later.UnixNano() {
		t.Fatalf("mtime not recorded: %d vs %d", f.ModTime, later.UnixNano())
	}
}

func (e *indexEnv) scope() string {
	_, _, scope, err := e.d.workspace()
	if err != nil {
		e.t.Fatal(err)
	}
	return scope
}

func TestReAddingUnchangedFilesIsANoOp(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a.txt", []byte("alpha"))
	e.write("b.txt", []byte("beta"))
	first := e.add(".")
	if first.Indexed != 2 {
		t.Fatalf("%+v", first)
	}
	second := e.add(".")
	if second.Unchanged != 2 || second.Indexed != 0 || second.Updated != 0 || len(second.Files) != 0 {
		t.Fatalf("%+v", second)
	}
}

func TestDirectoryScanSkipsWhatItShould(t *testing.T) {
	e := newIndexEnv(t)
	e.write("docs/a.txt", []byte("alpha in docs"))
	e.write("docs/deep/b.md", []byte("alpha in deep"))
	e.write("docs/.hidden/c.txt", []byte("alpha hidden"))
	e.write("docs/node_modules/d.txt", []byte("alpha dependency"))
	e.write("docs/image.png", []byte("\x89PNG not text"))
	e.write("other.txt", []byte("alpha outside"))
	out := e.add("docs")
	if out.Indexed != 2 {
		t.Fatalf("%+v", out)
	}
	if got := hitPaths(e.search("alpha")); strings.Contains(got, "hidden") || strings.Contains(got, "node_modules") || strings.Contains(got, "other") || strings.Contains(got, "png") {
		t.Fatalf("indexed what it should not: %s", got)
	}
	// A file named directly with an unsupported type is reported, not guessed at.
	r := e.add("docs/image.png")
	if statusOf(r, "docs/image.png") != "skipped" {
		t.Fatalf("%+v", r)
	}
}

func TestDirectoryScanStopsAtTheFileLimitAndSaysSo(t *testing.T) {
	e := newIndexEnv(t)
	for i := 0; i < docIndexMaxFiles+5; i++ {
		e.write(fmt.Sprintf("bulk/f%03d.txt", i), []byte(fmt.Sprintf("document number %d", i)))
	}
	out := e.add("bulk")
	if !out.Truncated || out.Indexed != docIndexMaxFiles {
		t.Fatalf("indexed=%d truncated=%v", out.Indexed, out.Truncated)
	}
	if len(out.Files) > docIndexMaxReported {
		t.Fatalf("result listed %d rows, cap is %d", len(out.Files), docIndexMaxReported)
	}
	// Running it again continues where it stopped; files already done cost nothing.
	again := e.add("bulk")
	if again.Truncated || again.Indexed != 5 || again.Unchanged != docIndexMaxFiles {
		t.Fatalf("second pass: %+v", again)
	}
}

func TestPathsOutsideTheWorkspaceAreRefused(t *testing.T) {
	e := newIndexEnv(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"../secret.txt", filepath.Join(outside, "secret.txt"), outside} {
		out := e.add(p)
		if out.Indexed != 0 || out.Failed != 1 {
			t.Errorf("%q: %+v", p, out)
		}
	}
	if out := e.search("secret"); len(out.Hits) != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestDirectorySymlinkOutOfWorkspaceIsNotFollowed(t *testing.T) {
	e := newIndexEnv(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.root, "link")); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(e.root, "link.txt")); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}
	e.write("inside.txt", []byte("inside text"))
	out := e.add(".")
	if out.Indexed != 1 || out.Failed != 0 {
		t.Fatalf("links are left out quietly, not indexed and not errors: %+v", out)
	}
	if got := e.search("secret"); len(got.Hits) != 0 {
		t.Fatalf("followed a link out of the workspace: %+v", got.Hits)
	}
}

func TestRemoveForgetsDirectoryButNeverTouchesFiles(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a/one.txt", []byte("alpha one"))
	e.write("a/two.txt", []byte("alpha two"))
	e.write("ab/three.txt", []byte("alpha three"))
	e.add("a", "ab")
	out, err := e.d.index(e.ctx, indexDocumentsInput{Action: "remove", Paths: []string{"a"}})
	if err != nil || out.Removed != 2 {
		t.Fatalf("%+v %v", out, err)
	}
	if got := hitPaths(e.search("alpha")); got != "ab/three.txt" {
		t.Fatalf("remove of a must not take ab: %s", got)
	}
	for _, f := range []string{"a/one.txt", "a/two.txt", "ab/three.txt"} {
		if _, err := os.Stat(filepath.Join(e.root, filepath.FromSlash(f))); err != nil {
			t.Fatalf("remove deleted a real file: %v", err)
		}
	}
	again, _ := e.d.index(e.ctx, indexDocumentsInput{Action: "remove", Paths: []string{"a"}})
	if statusOf(again, "a") != "not_indexed" {
		t.Fatalf("%+v", again)
	}
}

func TestWorkspacesHaveSeparateIndexes(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a.txt", []byte("alpha here"))
	e.add("a.txt")
	first := e.root
	e.root = t.TempDir()
	if out := e.search("alpha"); len(out.Hits) != 0 || !strings.Contains(out.Message, "no documents are indexed") {
		t.Fatalf("another workspace saw this index: %+v", out)
	}
	e.root = first
	if out := e.search("alpha"); len(out.Hits) != 1 {
		t.Fatalf("index lost on switching back: %+v", out)
	}
}

func TestScannedPDFIsReportedNotIndexed(t *testing.T) {
	e := newIndexEnv(t)
	e.write("scan.pdf", buildTestPDF([]string{"", ""}))
	out := e.add("scan.pdf")
	if out.Indexed != 0 || out.Skipped != 1 || !strings.Contains(strings.ToLower(statusReason(out, "scan.pdf")), "scan") {
		t.Fatalf("%+v", out)
	}
}

func statusReason(out indexDocumentsOutput, path string) string {
	for _, f := range out.Files {
		if f.Path == path {
			return f.Reason
		}
	}
	return ""
}

func TestFileThatBecomesEmptyIsDroppedFromTheIndex(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a.txt", []byte("alpha content"))
	e.add("a.txt")
	e.write("a.txt", []byte("   \n"))
	r := e.refresh()
	if statusOf(r, "a.txt") != "skipped" {
		t.Fatalf("%+v", r)
	}
	if list, _ := e.d.list(e.ctx, listIndexedInput{}); list.Total != 0 {
		t.Fatalf("stale content kept for an emptied file: %+v", list)
	}
}

func TestParseFailureKeepsOldEntryButHidesIt(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a.pdf", buildTestPDF([]string{"quarterly report numbers"}))
	e.add("a.pdf")
	e.write("a.pdf", []byte("not a pdf any more"))
	r := e.refresh()
	if statusOf(r, "a.pdf") != "failed" {
		t.Fatalf("%+v", r)
	}
	out := e.search("quarterly report")
	if len(out.Hits) != 0 || len(out.Stale) != 1 {
		t.Fatalf("a file that no longer parses must not answer from old content: %+v", out)
	}
	if list, _ := e.d.list(e.ctx, listIndexedInput{}); list.Total != 1 {
		t.Fatalf("a failed refresh must not delete the entry: %+v", list)
	}
}

func TestParserVersionChangeMakesEntryStale(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a.txt", []byte("alpha content"))
	e.add("a.txt")
	f, _, _ := e.d.store.Get(e.ctx, e.scope(), "a.txt")
	chunks := []docindex.Chunk{{ID: "x", Start: 0, End: 13, Text: "alpha content"}}
	f.ParserVersion = "agentgo-parse-v1"
	if err := e.d.store.Replace(e.ctx, f, chunks); err != nil {
		t.Fatal(err)
	}
	out := e.search("alpha")
	if len(out.Hits) != 0 || len(out.Stale) != 1 {
		t.Fatalf("offsets from another parser version must not be cited: %+v", out)
	}
	if r := e.refresh(); r.Updated != 1 {
		t.Fatalf("%+v", r)
	}
	if out := e.search("alpha"); len(out.Hits) != 1 {
		t.Fatalf("%+v", out)
	}
}

func TestChunkerPluginHeadersAreSearchableAndBadRangesAreRejected(t *testing.T) {
	e := newIndexEnv(t)
	text := "Section body about expenses only."
	e.write("a.txt", []byte(text))
	dispose := e.reg.SetDocumentChunker(func(text string, size int) DocumentChunks {
		return DocumentChunks{Children: []DocumentChunk{{Start: 0, End: len([]rune(text)), ContextHeader: "Handbook > Reimbursement"}}}
	})
	e.add("a.txt")
	out := e.search("reimbursement")
	if len(out.Hits) != 1 || out.Hits[0].Heading != "Handbook > Reimbursement" || out.Hits[0].Text != text {
		t.Fatalf("heading not searchable: %+v", out)
	}
	dispose()

	e.write("b.txt", []byte("beta content"))
	e.reg.SetDocumentChunker(func(text string, size int) DocumentChunks {
		return DocumentChunks{Children: []DocumentChunk{{Start: 0, End: 9999}}}
	})
	r := e.add("b.txt")
	if statusOf(r, "b.txt") != "failed" {
		t.Fatalf("an out-of-range chunk must fail the file: %+v", r)
	}
	if got := e.search("beta"); len(got.Hits) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestLongChunkIsCutWithCitationMatchingTheCutText(t *testing.T) {
	e := newIndexEnv(t)
	body := strings.Repeat("长", 2000) + " needle"
	e.write("long.txt", []byte(body))
	e.reg.SetDocumentChunker(func(text string, size int) DocumentChunks {
		return DocumentChunks{Children: []DocumentChunk{{Start: 0, End: len([]rune(text))}}}
	})
	e.add("long.txt")
	out := e.search("needle")
	if len(out.Hits) != 1 {
		t.Fatalf("%+v", out)
	}
	h := out.Hits[0]
	if !h.Truncated || len([]rune(h.Text)) != docSearchHitMaxRunes || h.Citation == nil || h.Citation.End-h.Citation.Start != docSearchHitMaxRunes {
		t.Fatalf("truncated=%v len=%d citation=%+v", h.Truncated, len([]rune(h.Text)), h.Citation)
	}
	assertCitationMatchesParseFile(t, e, h)
}

func TestPlainChunksCoverTextWithoutOverlap(t *testing.T) {
	lines := []string{"row one", "row two is a bit longer", strings.Repeat("x", 1300), "last"}
	text := strings.Join(lines, "\n")
	runes := []rune(text)
	chunks := plainChunks(text, 100).Children
	prev := 0
	covered := 0 // letters and digits inside chunks; newlines between lines may sit inside one
	for _, c := range chunks {
		if c.Start < prev || c.End <= c.Start || c.End > len(runes) {
			t.Fatalf("bad chunk %+v after %d", c, prev)
		}
		if c.End-c.Start > 100 {
			t.Fatalf("chunk longer than size: %+v", c)
		}
		for _, r := range runes[prev:c.Start] {
			if r != '\n' {
				t.Fatalf("text between chunks was dropped: %q", string(runes[prev:c.Start]))
			}
		}
		for _, r := range runes[c.Start:c.End] {
			if r != '\n' {
				covered++
			}
		}
		prev = c.End
	}
	if covered != len(runes)-3 {
		t.Fatalf("chunks hold %d of %d non-newline runes", covered, len(runes)-3)
	}
	if len(plainChunks("", 100).Children) != 0 {
		t.Fatal("empty text has no chunks")
	}
}

func TestDescribeLocator(t *testing.T) {
	cases := []struct {
		spans []parsedSourceSpan
		want  string
	}{
		{[]parsedSourceSpan{{Type: "pdf", Page: 3}}, "page 3"},
		{[]parsedSourceSpan{{Type: "pdf", Page: 3}, {Type: "pdf", Page: 4}}, "pages 3-4"},
		{[]parsedSourceSpan{{Type: "sheet", Sheet: "S", RowStart: 2, RowEnd: 2}, {Type: "sheet", Sheet: "S", RowStart: 3, RowEnd: 3}}, `sheet "S" rows 2-3`},
		{[]parsedSourceSpan{{Type: "sheet", Sheet: "A", RowStart: 9, RowEnd: 9}, {Type: "sheet", Sheet: "B", RowStart: 1, RowEnd: 1}}, `sheet "A" row 9; sheet "B" row 1`},
		{[]parsedSourceSpan{{Type: "docx", Mapping: "parsed_text_only"}}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := describeLocator(c.spans); got != c.want {
			t.Errorf("%+v: got %q want %q", c.spans, got, c.want)
		}
	}
}

func TestSearchInputValidationAndPathFilter(t *testing.T) {
	e := newIndexEnv(t)
	e.write("a/x.txt", []byte("alpha in a"))
	e.write("b/y.txt", []byte("alpha in b"))
	e.add("a", "b")
	if _, err := e.d.search(e.ctx, searchDocumentsInput{Query: "  "}); err == nil {
		t.Error("empty query must fail")
	}
	if _, err := e.d.search(e.ctx, searchDocumentsInput{Query: "alpha", Limit: 99}); err == nil {
		t.Error("limit above the maximum must fail")
	}
	out, err := e.d.search(e.ctx, searchDocumentsInput{Query: "alpha", Path: "./b/"})
	if err != nil || hitPaths(out) != "b/y.txt" {
		t.Fatalf("%+v %v", out, err)
	}
	if _, err := e.d.search(e.ctx, searchDocumentsInput{Query: "alpha", Path: "../x"}); err == nil {
		t.Error("a path outside the workspace must fail")
	}
	one, _ := e.d.search(e.ctx, searchDocumentsInput{Query: "alpha", Limit: 1})
	if len(one.Hits) != 1 {
		t.Fatalf("limit ignored: %d", len(one.Hits))
	}
	e.root = ""
	if _, err := e.d.search(e.ctx, searchDocumentsInput{Query: "alpha"}); err == nil {
		t.Error("no workspace must fail")
	}
}

func TestDocumentIndexToolsAreDeclared(t *testing.T) {
	e := newIndexEnv(t)
	ts, err := DocumentIndexTools(e.reg, e.d.store, func() string { return e.root }, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range ts {
		info, err := tl.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, info.Name)
	}
	if strings.Join(names, ",") != strings.Join(DocIndexToolNames, ",") || len(names) != 4 {
		t.Fatalf("names = %v", names)
	}
	if DocIndexReadOnly("index_documents") || !DocIndexReadOnly("search_documents") || !DocIndexReadOnly("list_indexed_documents") {
		t.Fatal("only index_documents changes the index")
	}
}

func TestPDFChunksNeverStraddlePages(t *testing.T) {
	e := newIndexEnv(t)
	e.write("two.pdf", buildTestPDF([]string{"alpha topic on the first page", "beta topic on the second page"}))
	e.add("two.pdf")
	for q, want := range map[string]string{"alpha topic": "page 1", "beta topic": "page 2"} {
		out := e.search(q)
		if len(out.Hits) == 0 || out.Hits[0].Locator != want {
			t.Errorf("%q: %+v, want locator %q", q, out.Hits, want)
			continue
		}
		if strings.Contains(out.Hits[0].Text, "\n") {
			t.Errorf("%q: chunk text spans a page break: %q", q, out.Hits[0].Text)
		}
		assertCitationMatchesParseFile(t, e, out.Hits[0])
	}
}

// A chunk that is only part of a long text must still cite the same locators
// parse_file gives for that range: whole spans, not spans cut to the chunk.
func TestHitInsideLongTextCitesLikeParseFile(t *testing.T) {
	e := newIndexEnv(t)
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("line %02d filler text to make the document long enough for several chunks", i))
	}
	lines[45] = "line 45 the unique zebra clause"
	e.write("long.txt", []byte(strings.Join(lines, "\n")))
	e.add("long.txt")
	out := e.search("zebra clause")
	if len(out.Hits) != 1 || out.Hits[0].Citation == nil || out.Hits[0].Citation.Start == 0 {
		t.Fatalf("the hit should sit inside the document, not at its start: %+v", out)
	}
	assertCitationMatchesParseFile(t, e, out.Hits[0])
}

func TestGluedPDFTextIsFlaggedAtIndexing(t *testing.T) {
	if glued("An ordinary sentence about quarterly budget approval, with spaces between every word, repeated. " + strings.Repeat("Normal words here. ", 20)) {
		t.Error("ordinary text flagged")
	}
	if glued("出差报销单必须在返程后三十日内提交财务部审核。") {
		t.Error("Chinese text flagged")
	}
	if glued(strings.Repeat("Underreviewasaconferencepaperaticlr2027andsomemoretext ", 2)) {
		t.Error("two long runs are not enough to flag")
	}
	e := newIndexEnv(t)
	e.write("latex.txt", []byte(strings.Repeat("Underreviewasaconferencepaperaticlr2027andsomemoretext\n", 6)))
	e.write("plain.txt", []byte("A perfectly normal document."))
	out := e.add("latex.txt", "plain.txt")
	if !strings.Contains(statusReason(out, "latex.txt"), "word spacing") {
		t.Fatalf("no warning for glued text: %+v", out)
	}
	if statusReason(out, "plain.txt") != "" {
		t.Fatalf("warning on normal text: %+v", out)
	}
	list, _ := e.d.list(e.ctx, listIndexedInput{})
	var noted int
	for _, d := range list.Documents {
		if strings.Contains(d.Note, "word spacing") {
			noted++
		}
	}
	if noted != 1 {
		t.Fatalf("the warning must stay with the index entry: %+v", list)
	}
}

// Indexing, refreshing, listing and searching at the same time must neither
// deadlock on the application's single database connection nor race.
func TestConcurrentIndexingAndSearching(t *testing.T) {
	e := newIndexEnv(t)
	for i := 0; i < 12; i++ {
		e.write(fmt.Sprintf("c/f%02d.txt", i), []byte(fmt.Sprintf("shared keyword alpha and document %d", i)))
	}
	done := make(chan string, 16)
	run := func(name string, fn func() error) {
		go func() {
			for i := 0; i < 5; i++ {
				if err := fn(); err != nil {
					done <- name + ": " + err.Error()
					return
				}
			}
			done <- ""
		}()
	}
	run("add", func() error {
		_, err := e.d.index(e.ctx, indexDocumentsInput{Action: "add", Paths: []string{"c"}})
		return err
	})
	run("refresh", func() error { _, err := e.d.index(e.ctx, indexDocumentsInput{Action: "refresh"}); return err })
	run("search1", func() error { _, err := e.d.search(e.ctx, searchDocumentsInput{Query: "alpha"}); return err })
	run("search2", func() error { _, err := e.d.search(e.ctx, searchDocumentsInput{Query: "keyword document"}); return err })
	run("list", func() error { _, err := e.d.list(e.ctx, listIndexedInput{}); return err })
	run("remove", func() error {
		_, err := e.d.index(e.ctx, indexDocumentsInput{Action: "remove", Paths: []string{"c/f00.txt"}})
		return err
	})
	timeout := time.After(60 * time.Second)
	for i := 0; i < 6; i++ {
		select {
		case msg := <-done:
			if msg != "" {
				t.Error(msg)
			}
		case <-timeout:
			t.Fatal("deadlock: workers did not finish")
		}
	}
	// Whatever order they ran in, the index ends consistent: every row of the
	// full-text table belongs to a chunk, and the other way round.
	var chunks, terms int
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM doc_index_chunks`).Scan(&chunks)
	_ = e.db.QueryRow(`SELECT COUNT(*) FROM doc_index_fts`).Scan(&terms)
	if chunks != terms {
		t.Fatalf("chunks=%d fts rows=%d", chunks, terms)
	}
}

// A condition on one page and its conclusion on the next: the best hit holds
// only the condition, and the neighbour supplies the rest, from the same
// version of the same document and with a citation of its own.
func neighbourFixture(t *testing.T) *indexEnv {
	e := newIndexEnv(t)
	e.write("rules.pdf", buildTestPDF([]string{
		"Section one is the introduction to the purchasing handbook.",
		"Purchases above 500 yuan require director approval provided that",
		"the request is filed within 30 days of the purchase date.",
		"Section four lists contacts.",
	}))
	e.write("other.pdf", buildTestPDF([]string{"Unrelated first page of another document.", "Unrelated second page."}))
	e.add("rules.pdf", "other.pdf")
	return e
}

func TestSearchContextBringsTheNextPageOfTheSameDocument(t *testing.T) {
	e := neighbourFixture(t)
	plain, err := e.d.search(e.ctx, searchDocumentsInput{Query: "director approval purchases"})
	must(t, err)
	if len(plain.Hits) == 0 || plain.Hits[0].Locator != "page 2" || strings.Contains(plain.Hits[0].Text, "30 days") || plain.Hits[0].After != nil {
		t.Fatalf("setup: the hit alone must lack the conclusion: %+v", plain.Hits)
	}
	out, err := e.d.search(e.ctx, searchDocumentsInput{Query: "director approval purchases", Context: 1})
	must(t, err)
	h := out.Hits[0]
	if len(h.Before) != 1 || len(h.After) != 1 {
		t.Fatalf("before=%d after=%d", len(h.Before), len(h.After))
	}
	if h.After[0].Locator != "page 3" || !strings.Contains(h.After[0].Text, "30 days") || h.Before[0].Locator != "page 1" {
		t.Fatalf("neighbours: before=%+v after=%+v", h.Before[0], h.After[0])
	}
	for _, n := range []docHit{h.Before[0], h.After[0]} {
		if n.Path != "rules.pdf" {
			t.Fatalf("neighbour from another document: %+v", n)
		}
		if n.Citation == nil || n.Citation.Revision != h.Citation.Revision {
			t.Fatalf("neighbour must be cited at the hit's revision: %+v", n.Citation)
		}
		if n.ReadMore != nil {
			t.Fatal("neighbours carry no read_more")
		}
		assertNeighbourCitation(t, e, n)
	}
	if h.After[0].Citation.Start != h.Citation.End+1 {
		t.Fatalf("the next page starts right after the hit: hit ends %d, neighbour starts %d", h.Citation.End, h.After[0].Citation.Start)
	}
}

// assertNeighbourCitation is assertCitationMatchesParseFile for a hit without read_more.
func assertNeighbourCitation(t *testing.T, e *indexEnv, n docHit) {
	t.Helper()
	n.ReadMore = &docReadMore{RelativePath: n.Path, Offset: n.Citation.Start, ExpectedRevision: n.Citation.Revision}
	assertCitationMatchesParseFile(t, e, n)
}

func TestReadDocumentNeighborsTool(t *testing.T) {
	e := neighbourFixture(t)
	hit := e.search("director approval purchases").Hits[0]
	read := func(in neighborsInput) (neighborsOutput, error) { return e.d.neighbors(e.ctx, in) }

	out, err := read(neighborsInput{Path: "rules.pdf", ExpectedRevision: hit.Citation.Revision, Offset: hit.Citation.Start})
	must(t, err)
	if out.Center == nil || out.Center.Citation.ID != hit.Citation.ID || len(out.Before) != 1 || len(out.After) != 1 || !strings.Contains(out.After[0].Text, "30 days") {
		t.Fatalf("defaults give one each side: %+v", out)
	}
	if out.AtStart || out.AtEnd {
		t.Fatalf("not at an edge: %+v", out)
	}
	wide, err := read(neighborsInput{Path: "rules.pdf", ExpectedRevision: hit.Citation.Revision, Offset: hit.Citation.Start, Before: 3, After: 3})
	must(t, err)
	if len(wide.Before) != 1 || len(wide.After) != 2 || !wide.AtStart || !wide.AtEnd {
		t.Fatalf("a wide read stops at the document edges and says so: before=%d after=%d %+v", len(wide.Before), len(wide.After), wide)
	}
	for _, n := range append(append([]docHit{}, wide.Before...), wide.After...) {
		if n.Path != "rules.pdf" {
			t.Fatalf("crossed into %s", n.Path)
		}
	}
	onlyAfter, err := read(neighborsInput{Path: "rules.pdf", ExpectedRevision: hit.Citation.Revision, Offset: hit.Citation.Start, After: 1})
	must(t, err)
	if len(onlyAfter.Before) != 0 || len(onlyAfter.After) != 1 {
		t.Fatalf("after-only: %+v", onlyAfter)
	}

	bad := []struct {
		name string
		in   neighborsInput
	}{
		{"no revision", neighborsInput{Path: "rules.pdf", Offset: hit.Citation.Start}},
		{"wrong revision", neighborsInput{Path: "rules.pdf", ExpectedRevision: "deadbeef", Offset: hit.Citation.Start}},
		{"not indexed", neighborsInput{Path: "missing.pdf", ExpectedRevision: hit.Citation.Revision, Offset: 0}},
		{"offset in no chunk", neighborsInput{Path: "rules.pdf", ExpectedRevision: hit.Citation.Revision, Offset: 999999}},
		{"too many", neighborsInput{Path: "rules.pdf", ExpectedRevision: hit.Citation.Revision, Offset: hit.Citation.Start, After: docNeighborsMax + 1}},
		{"outside workspace", neighborsInput{Path: "../rules.pdf", ExpectedRevision: hit.Citation.Revision, Offset: 0}},
		{"directory", neighborsInput{Path: ".", ExpectedRevision: hit.Citation.Revision, Offset: 0}},
	}
	for _, b := range bad {
		if _, err := read(b.in); err == nil {
			t.Errorf("%s must fail", b.name)
		}
	}
}

func TestNeighborsRefuseAChangedDocument(t *testing.T) {
	e := neighbourFixture(t)
	hit := e.search("director approval purchases").Hits[0]
	in := neighborsInput{Path: "rules.pdf", ExpectedRevision: hit.Citation.Revision, Offset: hit.Citation.Start}
	e.write("rules.pdf", buildTestPDF([]string{"Everything was rewritten.", "Purchases above 900 yuan require director approval.", "No deadline."}))
	_, err := e.d.neighbors(e.ctx, in)
	if err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("a changed file must not give neighbours of the old version: %v", err)
	}
	e.refresh()
	_, err = e.d.neighbors(e.ctx, in)
	if err == nil || !strings.Contains(err.Error(), "source_changed") {
		t.Fatalf("after a refresh the old revision is gone, not silently replaced by the new one: %v", err)
	}
	fresh := e.search("director approval purchases").Hits[0]
	out, err := e.d.neighbors(e.ctx, neighborsInput{Path: "rules.pdf", ExpectedRevision: fresh.Citation.Revision, Offset: fresh.Citation.Start})
	if err != nil || len(out.After) != 1 || !strings.Contains(out.After[0].Text, "No deadline") {
		t.Fatalf("new version: %+v %v", out, err)
	}
}

func TestSearchContextValidationAndNeighbourBudget(t *testing.T) {
	e := newIndexEnv(t)
	if _, err := e.d.search(e.ctx, searchDocumentsInput{Query: "x", Context: docSearchMaxContext + 1}); err == nil {
		t.Error("context above the maximum must fail")
	}
	if _, err := e.d.search(e.ctx, searchDocumentsInput{Query: "x", Context: -1}); err == nil {
		t.Error("negative context must fail")
	}
	e.write("a.txt", []byte("needle first\n"+strings.Repeat("长", 1200)+"\nafter text"))
	e.reg.SetDocumentChunker(func(text string, size int) DocumentChunks {
		r := []rune(text)
		return DocumentChunks{Children: []DocumentChunk{{Start: 0, End: 12}, {Start: 13, End: 13 + 1200}, {Start: 13 + 1200 + 1, End: len(r)}}}
	})
	e.add("a.txt")
	out, err := e.d.search(e.ctx, searchDocumentsInput{Query: "needle", Context: 1})
	must(t, err)
	if len(out.Hits) != 1 || len(out.Hits[0].After) != 2-1 {
		t.Fatalf("%+v", out)
	}
	n := out.Hits[0].After[0]
	if !n.Truncated || len([]rune(n.Text)) != docNeighborMaxRunes || n.Citation == nil || n.Citation.End-n.Citation.Start != docNeighborMaxRunes {
		t.Fatalf("a long neighbour is cut to its budget with a matching citation: truncated=%v len=%d cite=%+v", n.Truncated, len([]rune(n.Text)), n.Citation)
	}
	assertNeighbourCitation(t, e, n)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type flagCall struct{ Path, State string }

// withFlagRecorder routes the index's source-change notices to a recorder that
// reports three marked memories per call.
func withFlagRecorder(e *indexEnv) *[]flagCall {
	calls := &[]flagCall{}
	e.d.onChange = func(ctx context.Context, path, state string) (int, error) {
		*calls = append(*calls, flagCall{path, state})
		return 3, nil
	}
	return calls
}

func TestRefreshMarksMemoriesLearnedFromChangedAndDeletedFiles(t *testing.T) {
	e := newIndexEnv(t)
	calls := withFlagRecorder(e)
	e.write("a.txt", []byte("alpha one"))
	e.write("b.txt", []byte("beta one"))
	e.write("c.txt", []byte("gamma one"))
	e.write("d.txt", []byte("delta one"))
	first := e.add(".")
	if len(*calls) != 0 || first.Flagged != 0 {
		t.Fatalf("indexing new files marks nothing: %+v %+v", first, *calls)
	}

	e.write("a.txt", []byte("alpha two"))
	if err := os.Remove(filepath.Join(e.root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(e.root, "c.txt"), later, later); err != nil { // touched, same content
		t.Fatal(err)
	}
	out := e.refresh()
	want := map[flagCall]bool{{"a.txt", "changed"}: true, {"b.txt", "deleted"}: true}
	if len(*calls) != 2 || !want[(*calls)[0]] || !want[(*calls)[1]] || (*calls)[0] == (*calls)[1] {
		t.Fatalf("calls = %+v, want exactly a changed and b deleted (not the touched c, not the untouched d)", *calls)
	}
	if out.Flagged != 6 || !strings.Contains(out.Message, "marked 6") {
		t.Fatalf("totals: %+v", out)
	}
	for _, f := range out.Files {
		if (f.Path == "a.txt" || f.Path == "b.txt") && f.Flagged != 3 {
			t.Errorf("%s flagged = %d", f.Path, f.Flagged)
		}
	}

	// Nothing changed since: a second refresh marks nothing more.
	*calls = nil
	if out := e.refresh(); len(*calls) != 0 || out.Flagged != 0 {
		t.Fatalf("second refresh: %+v %+v", out, *calls)
	}
}

func TestAddAlsoMarksAChangedFileButRemoveAndFailuresDoNot(t *testing.T) {
	e := newIndexEnv(t)
	calls := withFlagRecorder(e)
	e.write("a.txt", []byte("alpha one"))
	e.write("p.pdf", buildTestPDF([]string{"quarterly report numbers"}))
	e.write("e.txt", []byte("epsilon one"))
	e.add(".")

	e.write("a.txt", []byte("alpha two"))
	e.add("a.txt")
	if len(*calls) != 1 || (*calls)[0] != (flagCall{"a.txt", "changed"}) {
		t.Fatalf("add of a changed file: %+v", *calls)
	}

	*calls = nil
	e.write("p.pdf", []byte("not a pdf any more")) // fails to parse: nothing is known about it
	if r := e.refresh(); statusOf(r, "p.pdf") != "failed" || len(*calls) != 0 {
		t.Fatalf("a parse failure says nothing about the file: %+v %+v", r, *calls)
	}

	if _, err := e.d.index(e.ctx, indexDocumentsInput{Action: "remove", Paths: []string{"e.txt"}}); err != nil || len(*calls) != 0 {
		t.Fatalf("dropping a file from the index is not a change to the file: %v %+v", err, *calls)
	}

	// A file that becomes empty has changed too.
	e.write("a.txt", []byte("  \n"))
	e.refresh()
	if len(*calls) != 1 || (*calls)[0] != (flagCall{"a.txt", "changed"}) {
		t.Fatalf("emptied file: %+v", *calls)
	}
}

func TestParserUpgradeWithoutAFileChangeMarksNothing(t *testing.T) {
	e := newIndexEnv(t)
	calls := withFlagRecorder(e)
	e.write("a.txt", []byte("alpha content"))
	e.add("a.txt")
	f, _, _ := e.d.store.Get(e.ctx, e.scope(), "a.txt")
	f.ParserVersion = "agentgo-parse-v1"
	if err := e.d.store.Replace(e.ctx, f, []docindex.Chunk{{ID: "x", Start: 0, End: 13, Text: "alpha content"}}); err != nil {
		t.Fatal(err)
	}
	if r := e.refresh(); r.Updated != 1 || len(*calls) != 0 {
		t.Fatalf("re-reading an unchanged file with a newer parser is not a change: %+v %+v", r, *calls)
	}
}

func TestMarkingFailureIsReportedNotFatal(t *testing.T) {
	e := newIndexEnv(t)
	e.d.onChange = func(ctx context.Context, path, state string) (int, error) { return 0, fmt.Errorf("memory is busy") }
	e.write("a.txt", []byte("alpha one"))
	e.add("a.txt")
	e.write("a.txt", []byte("alpha two"))
	r := e.refresh()
	if statusOf(r, "a.txt") != "updated" || !strings.Contains(statusReason(r, "a.txt"), "memory is busy") {
		t.Fatalf("the index is current and the problem is said: %+v", r)
	}
	if out := e.search("alpha two"); len(out.Hits) != 1 {
		t.Fatalf("index not updated: %+v", out)
	}
}

func TestRecallToolSaysWhenMemoriesCameFromAChangedFile(t *testing.T) {
	r := NewRegistry()
	recalled := []memory.Record{
		{ID: "1", Content: "fresh", Metadata: map[string]interface{}{}},
		{ID: "2", Content: "old lesson", Metadata: map[string]interface{}{"source_state": "changed"}},
		{ID: "3", Content: "gone lesson", Metadata: map[string]interface{}{"source_state": "deleted"}},
	}
	if err := RegisterRecallTool(r, func(ctx context.Context, q string, o memory.RecallOptions) ([]memory.Record, error) {
		return recalled, nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := r.InvokeJSON(context.Background(), "recall_memories", `{"query":"lessons"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "2 of these memories") || !strings.Contains(raw, "source_state") {
		t.Fatalf("no warning: %s", raw)
	}
	recalled = recalled[:1]
	raw, _ = r.InvokeJSON(context.Background(), "recall_memories", `{"query":"lessons"}`)
	if strings.Contains(raw, "since changed") {
		t.Fatalf("warning without a stale memory: %s", raw)
	}
}
