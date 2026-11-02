package codetools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"agentgo/internal/coderuntime"
)

// seedTree writes a small project under dir/tree with distinct modification
// times (file i is newer than file i-1, in name order), so "newest first" is
// deterministic.
func seedTree(t *testing.T, dir string) string {
	t.Helper()
	files := map[string]string{
		"main.go":               "package main\n// needle here\nfunc a_b() {}\n",
		"pkg/util.go":           "package pkg\n// needle again\n// a.b(c) literal\n",
		"pkg/deep/x.go":         "package deep\n",
		"docs/readme.md":        "# docs\nneedle in docs\n",
		"notes.txt":             "plain needle\n",
		".hidden.txt":           "hidden-only\n",
		"skip/ignored.txt":      "ignored-by-gitignore\n",
		".gitignore":            "skip/\n",
		"blob.bin":              "needle\x00\x01\x02binary",
		"node_modules/dep/i.js": "needle in node_modules\n",
		"build/out.txt":         "needle in build\n",
		"package-lock.json":     "needle in a lock file\n",
		"unicode.txt":           "héllo wörld needle\n",
		".agentgoignore":        "private/\n",
		"private/secret.txt":    "needle but private\n",
		"nested/.gitignore":     "local.txt\n",
		"nested/local.txt":      "needle but locally ignored\n",
		"nested/visible.txt":    "needle visible\n",
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	tree := filepath.Join(dir, "tree")
	for i, n := range names {
		p := filepath.Join(tree, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(files[n]), 0o644); err != nil {
			t.Fatal(err)
		}
		ts := time.Unix(1_700_000_000+int64(i)*10, 0)
		_ = os.Chtimes(p, ts, ts)
	}
	return tree
}

func walkerSearcher(dir string) *searcher {
	return &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Rg: func() string { return "" }})}
}

func slashed(p string) string { return filepath.ToSlash(p) }

func TestGrepFindsLinesAndSkipsWhatItShould(t *testing.T) {
	dir := t.TempDir()
	tree := seedTree(t, dir)
	out := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: tree})
	if out.IsError {
		t.Fatal(out.Content)
	}
	for _, want := range []string{"main.go", "pkg/util.go", "docs/readme.md", "notes.txt", "nested/visible.txt", "unicode.txt", "  2:4: // needle here"} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("missing %q in\n%s", want, out.Content)
		}
	}
	for _, unwanted := range []string{".hidden", "skip/", "blob.bin", "node_modules", "build/", "private/", "local.txt"} {
		if strings.Contains(out.Content, unwanted) {
			t.Errorf("must skip %q but found it in\n%s", unwanted, out.Content)
		}
	}
}

func TestGrepHeaderCountsMatchesAndFiles(t *testing.T) {
	dir := t.TempDir()
	tree := seedTree(t, dir)
	out := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: tree, Include: "*.go"})
	head, _, _ := strings.Cut(out.Content, "\n")
	if head != "2 matches in 2 files, newest file first." {
		t.Fatalf("header = %q", head)
	}
	one := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: "plain needle", Path: tree})
	if head, _, _ := strings.Cut(one.Content, "\n"); head != "1 match in 1 file, newest file first." {
		t.Fatalf("singular header = %q", head)
	}
}

func TestGrepListsNewestFileFirst(t *testing.T) {
	dir := t.TempDir()
	tree := seedTree(t, dir)
	out := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: tree}).Content
	// seedTree gives later names newer times: unicode.txt is the newest match, main.go the oldest.
	if strings.Index(out, "unicode.txt") > strings.Index(out, "main.go") {
		t.Fatalf("newer files must be listed first:\n%s", out)
	}
}

func TestGrepIncludeFiltersAndLiteralText(t *testing.T) {
	dir := t.TempDir()
	tree := seedTree(t, dir)
	s := walkerSearcher(dir)
	ctx := context.Background()

	if out := s.grep(ctx, "", "", grepArgs{Pattern: "needle", Path: tree, Include: "*.{md,txt}"}).Content; strings.Contains(out, ".go") || !strings.Contains(out, "readme.md") || !strings.Contains(out, "notes.txt") {
		t.Fatalf("brace include wrong:\n%s", out)
	}
	if out := s.grep(ctx, "", "", grepArgs{Pattern: "a.b(c)", Path: tree, LiteralText: true}).Content; !strings.Contains(out, "pkg/util.go") || strings.Contains(out, "main.go") {
		t.Fatalf("literal text wrong:\n%s", out)
	}
	if out := s.grep(ctx, "", "", grepArgs{Pattern: "(unclosed", Path: tree}); !out.IsError || !strings.Contains(out.Content, "regular expression") {
		t.Fatalf("a bad regex must be reported: %+v", out)
	}
	if out := s.grep(ctx, "", "", grepArgs{Pattern: "no-such-text-anywhere", Path: tree}); out.IsError || !strings.HasPrefix(out.Content, "No matches for") {
		t.Fatalf("no-match answer: %+v", out)
	}
}

func TestGrepReportsAUnicodeColumnInUTF16Units(t *testing.T) {
	if got := utf16Column("héllo", len("hé")); got != 3 {
		t.Fatalf("column after é = %d, want 3", got)
	}
	if got := utf16Column("a😀b", len("a😀")); got != 4 { // the emoji is two UTF-16 units
		t.Fatalf("column after an emoji = %d, want 4", got)
	}
	dir := t.TempDir()
	tree := seedTree(t, dir)
	out := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: tree, Include: "unicode.txt"}).Content
	if !strings.Contains(out, "  1:13: héllo wörld needle") {
		t.Fatalf("column for a line with accents:\n%s", out)
	}
}

func TestGlobFindsFilesByPatternNewestFirst(t *testing.T) {
	dir := t.TempDir()
	tree := seedTree(t, dir)
	s := walkerSearcher(dir)
	ctx := context.Background()

	out := s.glob(ctx, "", "", globArgs{Pattern: "**/*.go", Path: tree}).Content
	for _, want := range []string{"3 files match", "main.go", "pkg/util.go", "pkg/deep/x.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Index(out, "pkg/util.go") > strings.Index(out, "main.go") {
		t.Fatalf("newest first violated:\n%s", out)
	}
	if out := s.glob(ctx, "", "", globArgs{Pattern: "pkg/*.go", Path: tree}).Content; !strings.Contains(out, "1 file match") || strings.Contains(out, "deep") {
		t.Fatalf("directory prefix pattern:\n%s", out)
	}
	if out := s.glob(ctx, "", "", globArgs{Pattern: "*.nothing", Path: tree}).Content; !strings.HasPrefix(out, "No files match") {
		t.Fatalf("empty answer: %q", out)
	}
	// The model asked for this name, so a lock file is not hidden from glob.
	if out := s.glob(ctx, "", "", globArgs{Pattern: "package-lock.json", Path: tree}).Content; !strings.Contains(out, "package-lock.json") {
		t.Fatalf("an explicit file-name pattern must find noise-named files:\n%s", out)
	}
	if out := s.glob(ctx, "", "", globArgs{Pattern: "**/[", Path: tree}); !out.IsError {
		t.Fatalf("an invalid pattern must be rejected: %+v", out)
	}
}

func TestGlobAnchorsLiteralPrefixAndAbsolutePatterns(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "work")
	cases := []struct {
		pattern, wantRoot, wantRest string
	}{
		{"*.go", root, "*.go"},
		{"internal/agent/*.go", filepath.Join(root, "internal", "agent"), "*.go"},
		{"pkg/util.go", filepath.Join(root, "pkg"), "util.go"},
		{"main.go", root, "main.go"},
		{"**/*.go", root, "**/*.go"},
	}
	for _, c := range cases {
		gotRoot, gotRest := anchorGlob(root, c.pattern)
		if gotRoot != c.wantRoot || gotRest != c.wantRest {
			t.Errorf("anchorGlob(%q) = (%q, %q), want (%q, %q)", c.pattern, gotRoot, gotRest, c.wantRoot, c.wantRest)
		}
	}
	abs := slashed(filepath.Join(t.TempDir(), "src"))
	gotRoot, gotRest := anchorGlob(root, abs+"/**/*.go")
	if slashed(gotRoot) != abs || gotRest != "**/*.go" {
		t.Errorf("absolute pattern anchored to (%q, %q)", gotRoot, gotRest)
	}
}

func TestGlobPatternCannotLeaveTheWorkspaceUnapproved(t *testing.T) {
	dir := relativeWorkdir(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := 0
	s := &searcher{normalizeSearch(SearchOptions{
		WorkDir: dir, Rg: func() string { return "" },
		Approve: func(context.Context, coderuntime.FSRequest) bool { asked++; return false },
	})}
	// The search folder is inside the workspace; only the pattern's literal prefix points outside.
	out := s.glob(context.Background(), "", "", globArgs{Pattern: slashed(outside) + "/*.txt", Path: "."})
	if !out.IsError || strings.Contains(out.Content, "secret.txt") || asked == 0 {
		t.Fatalf("an absolute pattern outside the workspace was not gated (asked %d): %+v", asked, out)
	}
}

func TestLsShowsAnIndentedTreeFilesBeforeFolders(t *testing.T) {
	dir := t.TempDir()
	tree := seedTree(t, dir)
	s := walkerSearcher(dir)
	ctx := context.Background()

	out := s.ls(ctx, "", "", lsArgs{Path: tree}).Content
	root := slashed(tree) + "/\n"
	if !strings.HasPrefix(out, root) {
		t.Fatalf("listing must start with the root:\n%s", out)
	}
	for _, want := range []string{"\n  main.go\n", "\n  pkg/\n", "\n    util.go\n", "\n      x.go\n", "\n  .hidden.txt\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", strings.TrimSpace(want), out)
		}
	}
	for _, unwanted := range []string{"node_modules", "build/", "skip/", "private/", "package-lock.json"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("must not list %q:\n%s", unwanted, out)
		}
	}
	if strings.Index(out, "main.go") > strings.Index(out, "pkg/") {
		t.Fatalf("files must precede sub-folders:\n%s", out)
	}

	shallow := s.ls(ctx, "", "", lsArgs{Path: tree, Depth: 1}).Content
	if !strings.HasPrefix(shallow, "(Showing 1 level below") || strings.Contains(shallow, "util.go") {
		t.Fatalf("depth 1:\n%s", shallow)
	}
	if out := s.ls(ctx, "", "", lsArgs{Path: tree, Ignore: []string{"*.md", "docs"}}).Content; strings.Contains(out, "readme.md") {
		t.Fatalf("ignore globs not applied:\n%s", out)
	}
	if r := s.ls(ctx, "", "", lsArgs{Path: filepath.Join(tree, "missing")}); !r.IsError || !strings.Contains(r.Content, "No such folder") {
		t.Fatalf("missing folder: %+v", r)
	}
	if r := s.ls(ctx, "", "", lsArgs{Path: filepath.Join(tree, "main.go")}); !r.IsError || !strings.Contains(r.Content, "is a file") {
		t.Fatalf("a file is not a folder: %+v", r)
	}
	empty := filepath.Join(dir, "empty")
	_ = os.Mkdir(empty, 0o755)
	if out := s.ls(ctx, "", "", lsArgs{Path: empty}).Content; !strings.Contains(out, "(nothing to show)") {
		t.Fatalf("empty folder:\n%s", out)
	}
}

func TestLsStopsAtItsEntryLimit(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < lsLimit+50; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := walkerSearcher(dir).ls(context.Background(), "", "", lsArgs{}).Content
	if !strings.Contains(out, fmt.Sprintf("stopped after %d entries", lsLimit)) || strings.Contains(out, fmt.Sprintf("f%04d.txt", lsLimit+10)) {
		t.Fatalf("a capped listing must say so and stop:\n%s", out[:min(len(out), 400)])
	}
}

// A folder called "build" above the workspace must not hide the whole workspace.
func TestNoiseFoldersOnlyCountBelowTheSearchRoot(t *testing.T) {
	base := filepath.Join(t.TempDir(), "build", "proj")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "a.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := walkerSearcher(base).grep(context.Background(), "", "", grepArgs{Pattern: "needle"}).Content
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("the workspace lives under a folder named build and was hidden:\n%s", out)
	}
	if inNoiseDir(base, filepath.Join(base, "src", "a.go")) || !inNoiseDir(base, filepath.Join(base, "node_modules", "x", "a.js")) {
		t.Fatal("inNoiseDir must look only below the root")
	}
}

// With ripgrep installed, grep and glob must find the same files as the Go walker.
func TestRipgrepAndWalkerAgree(t *testing.T) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep not installed")
	}
	dir := t.TempDir()
	tree := seedTree(t, dir)
	opts := func(rgBin string) *searcher {
		return &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Rg: func() string { return rgBin }, GrepTimeout: 20 * time.Second, GlobTimeout: 20 * time.Second})}
	}
	withRg, walker := opts(rg), opts("")
	ctx := context.Background()

	fileSet := func(out string) string {
		var got []string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, slashed(tree)) {
				got = append(got, l)
			}
		}
		sort.Strings(got)
		return strings.Join(got, "\n")
	}
	g1 := withRg.grep(ctx, "", "", grepArgs{Pattern: "needle", Path: tree}).Content
	g2 := walker.grep(ctx, "", "", grepArgs{Pattern: "needle", Path: tree}).Content
	if a, b := fileSet(g1), fileSet(g2); a != b || a == "" {
		t.Errorf("grep files differ\nrg:\n%s\nwalker:\n%s", a, b)
	}
	l1 := withRg.glob(ctx, "", "", globArgs{Pattern: "**/*.go", Path: tree}).Content
	l2 := walker.glob(ctx, "", "", globArgs{Pattern: "**/*.go", Path: tree}).Content
	if a, b := fileSet(l1), fileSet(l2); a != b || a == "" {
		t.Errorf("glob files differ\nrg:\n%s\nwalker:\n%s", a, b)
	}
}
