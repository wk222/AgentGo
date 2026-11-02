package coderuntime

// Behaviour of view / write / edit / multiedit as the model sees it.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fsEnv struct {
	t     *testing.T
	dir   string
	tools []Tool
}

func newFSEnv(t *testing.T, opts FSOptions) *fsEnv {
	t.Helper()
	dir := t.TempDir()
	opts.WorkDir = dir
	opts.DisableBackup = true
	return &fsEnv{t: t, dir: dir, tools: NewFSTools(opts)}
}

func (e *fsEnv) path(name string) string { return filepath.Join(e.dir, name) }

func (e *fsEnv) seed(name, body string) string {
	e.t.Helper()
	p := e.path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *fsEnv) call(tool string, args map[string]any) ToolResult {
	e.t.Helper()
	r, err := fsTool(e.t, e.tools, tool).Run(context.Background(), "s", "c", fsArgs(args))
	if err != nil {
		e.t.Fatalf("%s: %v", tool, err)
	}
	return r
}

func (e *fsEnv) view(p string) ToolResult {
	e.t.Helper()
	return e.call("view", map[string]any{"file_path": p})
}

func (e *fsEnv) body(p string) string {
	e.t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func wantOK(t *testing.T, r ToolResult, contains ...string) {
	t.Helper()
	if r.IsError {
		t.Fatalf("unexpected error: %s", r.Content)
	}
	for _, c := range contains {
		if !strings.Contains(r.Content, c) {
			t.Fatalf("result lacks %q:\n%s", c, r.Content)
		}
	}
}

func wantErr(t *testing.T, r ToolResult, contains ...string) {
	t.Helper()
	if !r.IsError {
		t.Fatalf("expected an error, got: %s", r.Content)
	}
	for _, c := range contains {
		if !strings.Contains(r.Content, c) {
			t.Fatalf("error lacks %q:\n%s", c, r.Content)
		}
	}
}

// --- view ---------------------------------------------------------------------

func TestViewNumbersLinesAndSaysWhereItStopped(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	var b strings.Builder
	for i := 1; i <= 250; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	p := e.seed("long.txt", b.String())

	r := e.view(p)
	wantOK(t, r, "(lines 1-200)", "     1\tline 1\n", "   200\tline 200\n", "offset=200")
	if strings.Contains(r.Content, "line 201") {
		t.Fatal("the first slice must stop at 200 lines")
	}

	r = e.call("view", map[string]any{"file_path": p, "offset": 200})
	wantOK(t, r, "(lines 201-250)", "   250\tline 250\n")
	if strings.Contains(r.Content, "continues") {
		t.Fatalf("the last slice must not promise more:\n%s", r.Content)
	}

	r = e.call("view", map[string]any{"file_path": p, "offset": 10, "limit": 3})
	wantOK(t, r, "(lines 11-13)", "offset=13")
}

// FSOptions.ReadLimit (AGENTGO_VIEW_LINES) moves the default slice, and the
// tool description and schema must quote the same number the code applies.
func TestViewDefaultLinesFollowReadLimit(t *testing.T) {
	e := newFSEnv(t, FSOptions{ReadLimit: 30})
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	p := e.seed("long.txt", b.String())

	r := e.view(p)
	wantOK(t, r, "(lines 1-30)", "offset=30")
	if strings.Contains(r.Content, "line 31\n") {
		t.Fatal("the default slice must stop at the configured 30 lines")
	}
	wantOK(t, e.call("view", map[string]any{"file_path": p, "limit": 5}), "(lines 1-5)")

	view := fsTool(t, e.tools, "view")
	if !strings.Contains(view.Description, "You get 30 lines") {
		t.Fatalf("description quotes another default: %s", view.Description)
	}
	props, _ := view.Schema["properties"].(map[string]any)
	limit, _ := props["limit"].(map[string]any)
	if d, _ := limit["description"].(string); !strings.Contains(d, "default 30") {
		t.Fatalf("limit schema quotes another default: %q", d)
	}
}

func TestViewHandlesEmptyAndPastTheEnd(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	wantOK(t, e.view(e.seed("empty.txt", "")), "is empty")
	p := e.seed("short.txt", "a\nb\n")
	wantOK(t, e.call("view", map[string]any{"file_path": p, "offset": 50}), "no line after the first 50")
}

func TestViewCutsOverlongLinesAndRefusesOversizedSlices(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("wide.txt", strings.Repeat("x", MaxLineLength+500)+"\nshort\n")
	r := e.view(p)
	wantOK(t, r, "[line cut]", "short")
	if strings.Contains(r.Content, strings.Repeat("x", MaxLineLength+1)) {
		t.Fatal("an overlong line must be cut")
	}

	var b strings.Builder
	line := strings.Repeat("y", 1500)
	for b.Len() < MaxViewSize+4000 {
		b.WriteString(line + "\n")
	}
	big := e.seed("big.txt", b.String())
	wantErr(t, e.call("view", map[string]any{"file_path": big, "limit": 5000}), "over the", "limit")
}

func TestViewMissingDirectoryImageAndBinary(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	e.seed("config.json", "{}")
	wantErr(t, e.view(e.path("config.jsn")), "No such file")
	r := e.view(e.path("config"))
	wantErr(t, r, "No such file", "similar name", "config.json")

	wantErr(t, e.view(e.dir), "is a directory")
	wantErr(t, e.view(e.seed("pic.png", "not really")), "is an image")
	wantErr(t, e.view(e.seed("blob.bin", "ok\xff\xfe\x00bad")), "not valid UTF-8")
	wantErr(t, e.call("view", map[string]any{}), "file_path is required")
}

func TestViewOutsideTheWorkdirAsksFirst(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var asked []FSRequest
	allow := false
	e := newFSEnv(t, FSOptions{Approve: func(_ context.Context, r FSRequest) bool {
		asked = append(asked, r)
		return allow
	}})
	wantErr(t, e.view(outside), "declined")
	if len(asked) != 1 || asked[0].Action != "read" {
		t.Fatalf("expected one read approval, got %+v", asked)
	}
	allow = true
	wantOK(t, e.view(outside), "secret")
}

// --- write --------------------------------------------------------------------

func TestWriteCreatesFilesAndParentFolders(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.path("deep/er/new.txt")
	wantOK(t, e.call("write", map[string]any{"file_path": p, "content": "hello\n"}), "Created", "(6 bytes)")
	if got := e.body(p); got != "hello\n" {
		t.Fatalf("file holds %q", got)
	}
}

func TestWriteReplacingNeedsAFreshView(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.txt", "old\n")

	wantErr(t, e.call("write", map[string]any{"file_path": p, "content": "new\n"}), "has not been viewed")
	if e.body(p) != "old\n" {
		t.Fatal("a refused write must leave the file alone")
	}

	e.view(p)
	wantOK(t, e.call("write", map[string]any{"file_path": p, "content": "new\n"}), "Replaced")
	if e.body(p) != "new\n" {
		t.Fatal("the write did not land")
	}

	// Someone else touched it after our view.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.call("write", map[string]any{"file_path": p, "content": "third\n"}), "modified after you last viewed")
}

func TestWriteSkipsIdenticalContent(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("same.txt", "same\n")
	e.view(p)
	wantErr(t, e.call("write", map[string]any{"file_path": p, "content": "same\n"}), "already holds exactly this content")
}

func TestWriteRefusedByTheUserChangesNothing(t *testing.T) {
	e := newFSEnv(t, FSOptions{Approve: func(context.Context, FSRequest) bool { return false }})
	p := e.path("blocked.txt")
	wantErr(t, e.call("write", map[string]any{"file_path": p, "content": "x"}), "declined")
	if _, err := os.Stat(p); err == nil {
		t.Fatal("a declined write must not create the file")
	}
}

// --- edit ---------------------------------------------------------------------

func TestEditReplacesOneUniqueMatch(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.go", "package a\n\nfunc One() {}\nfunc Two() {}\n")
	e.view(p)
	wantOK(t, e.call("edit", map[string]any{"file_path": p, "old_string": "func One() {}", "new_string": "func Uno() {}"}),
		"Edited", "replaced 1 occurrence.")
	if got := e.body(p); got != "package a\n\nfunc Uno() {}\nfunc Two() {}\n" {
		t.Fatalf("file holds %q", got)
	}
}

func TestEditAmbiguousNeedsReplaceAll(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.txt", "x = 1\ny = 1\n")
	e.view(p)
	wantErr(t, e.call("edit", map[string]any{"file_path": p, "old_string": "= 1", "new_string": "= 2"}), "more than one place")
	if e.body(p) != "x = 1\ny = 1\n" {
		t.Fatal("an ambiguous edit must change nothing")
	}
	wantOK(t, e.call("edit", map[string]any{"file_path": p, "old_string": "= 1", "new_string": "= 2", "replace_all": true}), "replaced 2 occurrences.")
	if e.body(p) != "x = 2\ny = 2\n" {
		t.Fatalf("file holds %q", e.body(p))
	}
}

func TestEditDeletesWhenNewStringIsEmpty(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.txt", "keep\ndrop me\nkeep too\n")
	e.view(p)
	wantOK(t, e.call("edit", map[string]any{"file_path": p, "old_string": "drop me\n", "new_string": ""}), "removed 1 occurrence")
	if e.body(p) != "keep\nkeep too\n" {
		t.Fatalf("file holds %q", e.body(p))
	}
}

func TestEditWithEmptyOldStringCreatesButNeverClobbers(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.path("fresh.txt")
	wantOK(t, e.call("edit", map[string]any{"file_path": p, "old_string": "", "new_string": "born\n"}), "Created")
	if e.body(p) != "born\n" {
		t.Fatal("the new file has the wrong content")
	}
	wantErr(t, e.call("edit", map[string]any{"file_path": p, "old_string": "", "new_string": "again\n"}), "already exists")
}

func TestEditNeedsAViewAndReportsMissingText(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.txt", "alpha\nbeta\ngamma\n")
	wantErr(t, e.call("edit", map[string]any{"file_path": p, "old_string": "beta", "new_string": "B"}), "has not been viewed")
	e.view(p)
	wantErr(t, e.call("edit", map[string]any{"file_path": p, "old_string": "delta", "new_string": "D"}), "was not found")
	wantErr(t, e.call("edit", map[string]any{"file_path": p, "old_string": "beta", "new_string": "beta"}), "Nothing to do")
	wantErr(t, e.call("edit", map[string]any{"file_path": e.path("nope.txt"), "old_string": "a", "new_string": "b"}), "No such file")
}

func TestEditKeepsCRLFLineEndings(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("win.txt", "one\r\ntwo\r\nthree\r\n")
	e.view(p)
	wantOK(t, e.call("edit", map[string]any{"file_path": p, "old_string": "two\nthree", "new_string": "2\n3\n4"}))
	if got := e.body(p); got != "one\r\n2\r\n3\r\n4\r\n" {
		t.Fatalf("line endings were not preserved: %q", got)
	}
}

func TestEditToleratesTypographicPunctuationAndSaysSo(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.txt", "say(\"hi\")\n")
	e.view(p)
	r := e.call("edit", map[string]any{"file_path": p, "old_string": "say(\u201chi\u201d)", "new_string": "say(\"bye\")"})
	wantOK(t, r, "Note: old_string differed from the file only in")
	if e.body(p) != "say(\"bye\")\n" {
		t.Fatalf("file holds %q", e.body(p))
	}
}

func TestEditAppendsLanguageServerFeedback(t *testing.T) {
	e := newFSEnv(t, FSOptions{AfterChange: func(_ context.Context, _, _, path string) string {
		return "Problems in " + filepath.Base(path) + " (1 error)"
	}})
	p := e.seed("a.txt", "a\n")
	e.view(p)
	wantOK(t, e.call("edit", map[string]any{"file_path": p, "old_string": "a", "new_string": "b"}), "Edited", "Problems in a.txt (1 error)")
}

// --- multiedit ----------------------------------------------------------------

func TestMultiEditAppliesInOrderAndReportsTheFailures(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.txt", "one\ntwo\nthree\n")
	e.view(p)

	r := e.call("multiedit", map[string]any{"file_path": p, "edits": []map[string]any{
		{"old_string": "one", "new_string": "1"},
		{"old_string": "missing", "new_string": "?"},
		{"old_string": "1\ntwo", "new_string": "1\n2"}, // sees the first edit's result
	}})
	wantOK(t, r, "2 of 3 edits applied", "edit 2 not applied")
	if e.body(p) != "1\n2\nthree\n" {
		t.Fatalf("file holds %q", e.body(p))
	}

	r = e.call("multiedit", map[string]any{"file_path": p, "edits": []map[string]any{
		{"old_string": "nothing", "new_string": "x"},
	}})
	wantErr(t, r, "None of the 1 edits could be applied", "edit 1 not applied")
}

func TestMultiEditAllApplied(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.txt", "a b c\n")
	e.view(p)
	wantOK(t, e.call("multiedit", map[string]any{"file_path": p, "edits": []map[string]any{
		{"old_string": "a", "new_string": "A"},
		{"old_string": "c", "new_string": "C"},
	}}), "all 2 edits applied")
	if e.body(p) != "A b C\n" {
		t.Fatalf("file holds %q", e.body(p))
	}
}

func TestMultiEditCreatesFromTheFirstEdit(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.path("made.txt")
	wantOK(t, e.call("multiedit", map[string]any{"file_path": p, "edits": []map[string]any{
		{"old_string": "", "new_string": "hello world\n"},
		{"old_string": "world", "new_string": "there"},
	}}), "Created", "from 2 edits")
	if e.body(p) != "hello there\n" {
		t.Fatalf("file holds %q", e.body(p))
	}
}

func TestMultiEditRejectsMisplacedEmptyOldString(t *testing.T) {
	e := newFSEnv(t, FSOptions{})
	p := e.seed("a.txt", "a\n")
	e.view(p)
	wantErr(t, e.call("multiedit", map[string]any{"file_path": p, "edits": []map[string]any{
		{"old_string": "a", "new_string": "b"},
		{"old_string": "", "new_string": "c"},
	}}), "edit 2 has an empty old_string")
	wantErr(t, e.call("multiedit", map[string]any{"file_path": p, "edits": []map[string]any{}}), "edits is empty")
	if e.body(p) != "a\n" {
		t.Fatal("a rejected request must change nothing")
	}
}

func TestMutationsAskForApprovalWithADescription(t *testing.T) {
	var got []FSRequest
	e := newFSEnv(t, FSOptions{Approve: func(_ context.Context, r FSRequest) bool {
		got = append(got, r)
		return true
	}})
	p := e.seed("a.txt", "x\n")
	e.view(p)
	e.call("edit", map[string]any{"file_path": p, "old_string": "x", "new_string": "y"})
	if len(got) != 1 || got[0].Tool != "edit" || got[0].Path == "" || got[0].Description == "" {
		t.Fatalf("approval request malformed: %+v", got)
	}
}
