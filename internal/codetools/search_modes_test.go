package codetools

// grep's output modes, paging and context, and the limits the user can set.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// limitedSearcher is a walker-only searcher whose pages hold `limit` results.
func limitedSearcher(dir string, limit int) *searcher {
	return &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Rg: func() string { return "" }, Limit: limit})}
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// nonEmptyLines splits s into its lines, leaving out blank ones (paths may hold spaces).
func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func lineOf(out, want string) int {
	for i, l := range strings.Split(out, "\n") {
		if strings.Contains(l, want) {
			return i
		}
	}
	return -1
}

func TestGrepCountGivesTotalsPerFileMostFirst(t *testing.T) {
	dir := t.TempDir()
	writeLines(t, filepath.Join(dir, "a.txt"), "hit", "x", "hit", "hit")
	writeLines(t, filepath.Join(dir, "b.txt"), "hit")
	writeLines(t, filepath.Join(dir, "c.txt"), "hit", "hit", "none")

	out := limitedSearcher(dir, 0).grep(context.Background(), "", "", grepArgs{Pattern: "hit", Path: dir, OutputMode: "count"})
	if out.IsError {
		t.Fatal(out.Content)
	}
	head, _, _ := strings.Cut(out.Content, "\n")
	if head != "6 matches in 3 files, files with the most matches first." {
		t.Fatalf("header = %q", head)
	}
	a, c, b := lineOf(out.Content, "a.txt: 3"), lineOf(out.Content, "c.txt: 2"), lineOf(out.Content, "b.txt: 1")
	if a < 0 || c < 0 || b < 0 || !(a < c && c < b) {
		t.Fatalf("files are not listed by match count (a=%d c=%d b=%d):\n%s", a, c, b, out.Content)
	}
	if strings.Contains(out.Content, "  1:") {
		t.Fatalf("count mode must not print match lines:\n%s", out.Content)
	}
}

func TestGrepFilesModeListsOnlyPathsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	tree := seedTree(t, dir)
	out := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: tree, OutputMode: "files"}).Content
	head, rest, _ := strings.Cut(out, "\n")
	if head != "6 files match, newest first." {
		t.Fatalf("header = %q", head)
	}
	paths := nonEmptyLines(rest)
	if len(paths) != 6 || !strings.HasSuffix(paths[0], "unicode.txt") || !strings.HasSuffix(paths[5], "docs/readme.md") {
		t.Fatalf("paths = %v", paths)
	}
	if strings.Contains(out, "needle") {
		t.Fatalf("files mode must not print line text:\n%s", out)
	}
}

func TestGrepOffsetPagesThroughResults(t *testing.T) {
	dir := t.TempDir()
	rows := make([]string, 8)
	for i := range rows {
		rows[i] = fmt.Sprintf("row %d hit", i+1)
	}
	writeLines(t, filepath.Join(dir, "rows.txt"), rows...)
	s := limitedSearcher(dir, 3)
	grep := func(offset int) string {
		return s.grep(context.Background(), "", "", grepArgs{Pattern: "hit", Path: dir, Offset: offset}).Content
	}

	first := grep(0)
	head, _, _ := strings.Cut(first, "\n")
	if !strings.HasPrefix(head, "Matches 1-3 of 8,") || !strings.Contains(head, "truncated") {
		t.Fatalf("first page header = %q", head)
	}
	if !strings.Contains(first, "offset=3") || !strings.Contains(first, "row 3 hit") || strings.Contains(first, "row 4 hit") {
		t.Fatalf("first page:\n%s", first)
	}

	second := grep(3)
	if !strings.HasPrefix(second, "Matches 4-6 of 8,") || !strings.Contains(second, "row 4 hit") || !strings.Contains(second, "offset=6") || strings.Contains(second, "row 3 hit") {
		t.Fatalf("second page:\n%s", second)
	}

	last := grep(6)
	head, _, _ = strings.Cut(last, "\n")
	if head != "Matches 7-8 of 8, newest file first." || strings.Contains(last, "truncated") || strings.Contains(last, "Next page") {
		t.Fatalf("last page:\n%s", last)
	}

	if past := grep(20); !strings.HasPrefix(past, "Nothing at offset 20: the search found only 8 matches.") {
		t.Fatalf("past the end:\n%s", past)
	}
}

func TestGrepContextMergesTouchingBlocksAndMarksMatches(t *testing.T) {
	dir := t.TempDir()
	rows := make([]string, 10)
	for i := range rows {
		rows[i] = fmt.Sprintf("row %d", i+1)
	}
	writeLines(t, filepath.Join(dir, "rows.txt"), rows...)

	out := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: `row [359]$`, Path: dir, Context: 1}).Content
	for _, want := range []string{"  2- row 2", "  3:1: row 3", "  4- row 4", "  5:1: row 5", "  6- row 6", "  8- row 8", "  9:1: row 9", "  10- row 10", "is context"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	// 3 and 5 share row 4, so rows 2-6 are one block; row 9 starts another.
	sep := lineOf(out, "  --")
	if sep < lineOf(out, "  6- row 6") || sep > lineOf(out, "  8- row 8") || strings.Count(out, "  --") != 1 {
		t.Fatalf("blocks are not divided once between rows 6 and 8:\n%s", out)
	}
	if strings.Contains(out, "  1- row 1") || strings.Contains(out, "  7- row 7") {
		t.Fatalf("rows outside the context must stay out:\n%s", out)
	}
	if got := strings.Count(out, "row 4"); got != 1 {
		t.Fatalf("a row shared by two blocks must print once, got %d:\n%s", got, out)
	}

	// More than maxContext is clamped, not refused.
	wide := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: `row 1$`, Path: dir, Context: 99}).Content
	if !strings.Contains(wide, "  6- row 6") || strings.Contains(wide, "  7- row 7") {
		t.Fatalf("context must be clamped to %d lines:\n%s", maxContext, wide)
	}
}

func TestGrepRejectsAnUnknownOutputMode(t *testing.T) {
	dir := t.TempDir()
	out := walkerSearcher(dir).grep(context.Background(), "", "", grepArgs{Pattern: "x", Path: dir, OutputMode: "json"})
	if !out.IsError || !strings.Contains(out.Content, "output_mode") {
		t.Fatalf("got %+v", out)
	}
}

// A search that stopped early must not pass its count off as the total.
func TestCappedAnswersAreLowerBounds(t *testing.T) {
	var ms []grepMatch
	for i := 1; i <= 5; i++ {
		ms = append(ms, grepMatch{path: "/p/a.txt", modTime: time.Unix(10, 0), lineNum: i, charNum: 1, lineText: "hit"})
	}
	res := searchResult{matches: ms, capped: true}

	count := renderCount(res, 0, 100)
	if !strings.HasPrefix(count, "At least 5 matches in at least 1 file,") || !strings.Contains(count, "lower bounds") {
		t.Fatalf("count: %s", count)
	}
	content := renderContent(res, 0, 3, 0)
	head, _, _ := strings.Cut(content, "\n")
	if !strings.HasPrefix(head, "Matches 1-3 of at least 5,") || !strings.Contains(head, "truncated") || !strings.Contains(content, "Narrow the search") {
		t.Fatalf("content: %s", content)
	}
	// Nothing left after the last gathered match, yet the search was capped: no "next page", only "narrow".
	tail := renderContent(res, 3, 3, 0)
	if strings.Contains(tail, "Next page") || !strings.Contains(tail, "Narrow the search") {
		t.Fatalf("tail: %s", tail)
	}
	if past := renderContent(res, 40, 3, 0); !strings.Contains(past, "before it stopped") {
		t.Fatalf("past: %s", past)
	}
}

func TestSearchLimitSizesGlobAndGrepPages(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 7; i++ {
		writeLines(t, filepath.Join(dir, fmt.Sprintf("f%d.txt", i)), "hit")
	}
	s := limitedSearcher(dir, 3)

	glob := s.glob(context.Background(), "", "", globArgs{Pattern: "*.txt", Path: dir}).Content
	head, rest, _ := strings.Cut(glob, "\n")
	if !strings.Contains(head, "truncated") || !strings.Contains(head, "more than 3") || len(nonEmptyLines(rest)) != 3 {
		t.Fatalf("glob:\n%s", glob)
	}
	if d := s.globTool().Description; !strings.Contains(d, "at most 3 are returned") {
		t.Fatalf("glob description: %s", d)
	}
	if d := s.grepTool().Description; !strings.Contains(d, "3 matches per page") {
		t.Fatalf("grep description: %s", d)
	}
}

func TestConfiguredSearchReadsTheFlagsAndClampsThem(t *testing.T) {
	t.Setenv("AGENTGO_GREP_TIMEOUT_SEC", "12")
	t.Setenv("AGENTGO_GLOB_TIMEOUT_SEC", "0")
	t.Setenv("AGENTGO_SEARCH_LIMIT", "250")
	got := ConfiguredSearch(SearchOptions{})
	if got.GrepTimeout != 12*time.Second || got.GlobTimeout != time.Second || got.Limit != 250 {
		t.Fatalf("got timeouts %v/%v, limit %d", got.GrepTimeout, got.GlobTimeout, got.Limit)
	}

	t.Setenv("AGENTGO_SEARCH_LIMIT", "99999")
	t.Setenv("AGENTGO_GREP_TIMEOUT_SEC", "100000")
	if got := ConfiguredSearch(SearchOptions{}); got.Limit != 500 || got.GrepTimeout != 300*time.Second {
		t.Fatalf("upper clamp: limit %d, timeout %v", got.Limit, got.GrepTimeout)
	}
	t.Setenv("AGENTGO_SEARCH_LIMIT", "-4")
	if got := ConfiguredSearch(SearchOptions{}); got.Limit != 20 {
		t.Fatalf("lower clamp: limit %d", got.Limit)
	}

	// What the caller already set wins over the environment.
	if got := ConfiguredSearch(SearchOptions{Limit: 7, GrepTimeout: time.Minute}); got.Limit != 7 || got.GrepTimeout != time.Minute {
		t.Fatalf("explicit options were overridden: %+v", got)
	}
	// Unset flags give the documented defaults.
	t.Setenv("AGENTGO_SEARCH_LIMIT", "")
	t.Setenv("AGENTGO_GREP_TIMEOUT_SEC", "")
	t.Setenv("AGENTGO_GLOB_TIMEOUT_SEC", "")
	if got := ConfiguredSearch(SearchOptions{}); got.Limit != 100 || got.GrepTimeout != 5*time.Second || got.GlobTimeout != 30*time.Second {
		t.Fatalf("defaults: %+v", got)
	}
}

// The new grep arguments must reach the model: the adapter turns the schema
// (enum included) into a tool definition without complaint.
func TestGrepSchemaAdaptsWithTheNewArguments(t *testing.T) {
	tools := NativeSearch(SearchOptions{WorkDir: t.TempDir()})
	if _, err := Adapt(tools); err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Name != "grep" {
			continue
		}
		props, _ := tl.Schema["properties"].(map[string]any)
		for _, k := range []string{"output_mode", "offset", "context"} {
			if _, ok := props[k]; !ok {
				t.Fatalf("grep schema lacks %q", k)
			}
		}
		return
	}
	t.Fatal("no grep tool")
}

// With ripgrep installed, the count and file lists must match the Go walker's.
func TestModesAgreeBetweenRipgrepAndWalker(t *testing.T) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep not installed")
	}
	dir := t.TempDir()
	tree := seedTree(t, dir)
	mk := func(bin string) *searcher {
		return &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Rg: func() string { return bin }, GrepTimeout: 20 * time.Second})}
	}
	for _, mode := range []string{"count", "files"} {
		a := mk(rg).grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: tree, OutputMode: mode}).Content
		b := mk("").grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: tree, OutputMode: mode}).Content
		if a != b || a == "" {
			t.Errorf("%s differs\nrg:\n%s\nwalker:\n%s", mode, a, b)
		}
	}
}
