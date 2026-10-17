package codetools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	ct "agentgo/third_party/crush/pkg/codetools"
	einotool "github.com/cloudwego/eino/components/tool"

	"agentgo/internal/coderuntime"
)

// Parity (plan-crush-digestion D2): the native file tools must tell the model
// exactly what the Crush ones did for the same calls, and leave the same bytes
// on disk. The scenario below is run against both and the transcripts compared.

type step struct {
	tool string
	args func(dir string) string
}

func jsonPath(dir, name string) string { return quote(filepath.Join(dir, name)) }

// runToolLoose returns the tool's output, or its error text: the model sees
// both, so both are compared.
func runToolLoose(t *testing.T, tools map[string]einotool.BaseTool, name, args string) string {
	t.Helper()
	tl, ok := tools[name].(invokable)
	if !ok {
		t.Fatalf("tool %s missing or not invokable", name)
	}
	out, err := tl.InvokableRun(context.Background(), args)
	if err != nil {
		return "ERR: " + err.Error()
	}
	return out
}

// normalizeLS makes ls output order-insensitive and drops the depth banner.
// Both are deliberate differences: Crush's fastwalk emitted sibling
// directories in an arbitrary order under a depth limit, and printed the depth
// banner only when the depth came from its config, never from the argument.
func normalizeLS(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "The directory tree is shown up to a depth") {
			continue
		}
		keep = append(keep, l)
	}
	sort.Strings(keep)
	return strings.Join(keep, "\n")
}

// stripFailedEditLines removes the one deliberate difference: native multiedit
// lists each failed edit's reason in the result, where Crush only counted them
// (the reasons sat in metadata the model never saw).
func stripFailedEditLines(out string) string {
	var keep []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "edit ") && strings.Contains(l, " failed: ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.Join(keep, "\n")
}

func parityScenario() []step {
	view := func(name, extra string) step {
		return step{"code_view", func(d string) string { return `{"file_path":` + jsonPath(d, name) + extra + `}` }}
	}
	edit := func(name, old, new string, all bool) step {
		return step{"code_edit", func(d string) string {
			a := `{"file_path":` + jsonPath(d, name) + `,"old_string":` + quote(old) + `,"new_string":` + quote(new)
			if all {
				a += `,"replace_all":true`
			}
			return a + `}`
		}}
	}
	write := func(name, content string) step {
		return step{"code_write", func(d string) string {
			return `{"file_path":` + jsonPath(d, name) + `,"content":` + quote(content) + `}`
		}}
	}
	multi := func(name string, ops ...[2]string) step {
		return step{"code_multiedit", func(d string) string {
			var parts []string
			for _, o := range ops {
				parts = append(parts, `{"old_string":`+quote(o[0])+`,"new_string":`+quote(o[1])+`}`)
			}
			return `{"file_path":` + jsonPath(d, name) + `,"edits":[` + strings.Join(parts, ",") + `]}`
		}}
	}
	return []step{
		// read-before-modify
		edit("a.go", "alpha", "ALPHA", false),
		write("a.go", "overwritten\n"),
		view("a.go", ""),
		// plain, ambiguous, missing
		edit("a.go", "alpha", "ALPHA", false),
		edit("a.go", "dup", "DUP", false),
		edit("a.go", "dup", "DUP", true),
		edit("a.go", "does not exist anywhere", "x", false),
		edit("a.go", "ALPHA", "ALPHA", false),
		// whitespace-tolerant match and hint
		view("ws.go", ""),
		edit("ws.go", "if x {\n    return 1\n}", "if x {\n    return 2\n}", false),
		edit("ws.go", "if y {\nreturn 9\n}", "z", false),
		view("ws2.go", ""),
		edit("ws2.go", "  x := 1", "z := 3", false),
		// delete and create
		edit("a.go", "line three\n", "", false),
		edit("a.go", "", "brand new\n", false),
		edit("new/dir/c.txt", "", "created\n", false),
		view("a.go", ""),
		// pagination
		view("long.txt", `,"offset":5,"limit":3`),
		view("long.txt", `,"offset":500`),
		view("missing.txt", ""),
		view("a", ""),
		// multiedit with one failing edit
		view("m.txt", ""),
		multi("m.txt", [2]string{"one", "1"}, [2]string{"nope", "x"}, [2]string{"three", "3"}),
		multi("m.txt", [2]string{"nope", "x"}),
		multi("fresh.txt", [2]string{"", "seed\n"}, [2]string{"seed", "grown"}),
		// write after view, identical content
		view("w.txt", ""),
		write("w.txt", "changed\n"),
		write("w.txt", "changed\n"),
		// CRLF survives an edit
		view("crlf.txt", ""),
		edit("crlf.txt", "second", "SECOND", false),
	}
}

// searchScenario exercises grep / glob / ls on the tree built by seedSearch.
func searchScenario() []step {
	raw := func(tool, tmpl string) step {
		return step{tool, func(d string) string { return strings.ReplaceAll(tmpl, "$D", filepath.ToSlash(d)) }}
	}
	return []step{
		raw("code_grep", `{"pattern":"needle","path":"$D/tree"}`),
		raw("code_grep", `{"pattern":"needle","path":"$D/tree","include":"*.go"}`),
		raw("code_grep", `{"pattern":"needle","path":"$D/tree","include":"*.{md,txt}"}`),
		raw("code_grep", `{"pattern":"a.b(c)","path":"$D/tree","literal_text":true}`),
		raw("code_grep", `{"pattern":"no-such-text-anywhere","path":"$D/tree"}`),
		raw("code_grep", `{"pattern":"(unclosed","path":"$D/tree"}`),
		raw("code_grep", `{"pattern":"hidden-only","path":"$D/tree"}`),
		raw("code_grep", `{"pattern":"ignored-by-gitignore","path":"$D/tree"}`),
		raw("code_glob", `{"pattern":"**/*.go","path":"$D/tree"}`),
		raw("code_glob", `{"pattern":"pkg/*.go","path":"$D/tree"}`),
		raw("code_glob", `{"pattern":"*.nothing","path":"$D/tree"}`),
		raw("code_ls", `{"path":"$D/tree"}`),
		raw("code_ls", `{"path":"$D/tree","depth":1}`),
		raw("code_ls", `{"path":"$D/tree","ignore":["*.md"]}`),
		raw("code_ls", `{"path":"$D/tree/missing"}`),
	}
}

func seedSearch(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"tree/main.go":          "package main\n// needle here\nfunc a_b() {}\n",
		"tree/pkg/util.go":      "package pkg\n// needle again\n// a.b(c) literal\n",
		"tree/pkg/deep/x.go":    "package deep\n",
		"tree/docs/readme.md":   "# docs\nneedle in docs\n",
		"tree/notes.txt":        "plain needle\n",
		"tree/.hidden.txt":      "hidden-only\n",
		"tree/skip/ignored.txt": "ignored-by-gitignore\n",
		"tree/.gitignore":       "skip/\n",
		"tree/blob.bin":         "needle\x00\x01\x02binary",
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		body := files[name]
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		// Distinct modification times keep "newest first" deterministic.
		ts := time.Unix(1_700_000_000+int64(i)*10, 0)
		_ = os.Chtimes(p, ts, ts)
	}
}

func seedParity(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"a.go":     "alpha\ndup\nline three\ndup\n",
		"ws.go":    "func f() {\n\tif x {\n\t\treturn 1\n\t}\n}\n",
		"ws2.go":   "\tx := 1\n\ty := 2\n\tx := 1\n",
		"m.txt":    "one\ntwo\nthree\n",
		"w.txt":    "original\n",
		"crlf.txt": "first\r\nsecond\r\nthird\r\n",
	}
	var long strings.Builder
	for i := 1; i <= 20; i++ {
		long.WriteString("row\n")
	}
	files["long.txt"] = long.String()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runParity(t *testing.T, native bool, seed func(*testing.T, string), scenario []step) (transcript []string, disk map[string]string) {
	t.Helper()
	dir, tools := baselineTools(t, native)
	seed(t, dir)
	for _, s := range scenario {
		out := runToolLoose(t, tools, s.tool, s.args(dir))
		if native {
			out = stripFailedEditLines(out)
		}
		out = strings.ReplaceAll(strings.ReplaceAll(out, dir, "<D>"), filepath.ToSlash(dir), "<D>")
		if s.tool == "code_ls" {
			out = normalizeLS(out)
		}
		transcript = append(transcript, out)
	}
	disk = map[string]string{}
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(dir, p)
			disk[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	return transcript, disk
}

func TestNativeFSParityWithCrush(t *testing.T) {
	assertParity(t, seedParity, parityScenario(), []string{
		"you must read the file before editing", "has been modified since it was last read",
		"Content replaced in file", "Content deleted from file", "File created:",
		"appears multiple times", "old_string not found", "whitespace-equivalent",
		"A whitespace-normalized match", "File has more lines", "File not found",
		"is a directory", "Applied 2 of 3 edits", "File created with", "already contains the exact content",
	})
}

func TestNativeSearchParityWithCrush(t *testing.T) {
	assertParity(t, seedSearch, searchScenario(), []string{
		"Found ", "Line 2, Char ", "No files found", "invalid regex", "path does not exist",
		"<D>/tree/pkg/util.go", "- <D>/tree/",
	})
}

// Crush's Go-walker fallback searched node_modules (it checked directories
// with the file rule, which has no "node_modules"); the native walker never
// descends into always-ignored directories.
func TestNativeSearchSkipsNoiseDirs(t *testing.T) {
	dir, tools := baselineTools(t, true)
	seedSearch(t, dir)
	p := filepath.Join(dir, "tree", "node_modules", "dep", "i.js")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("needle in node_modules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.ToSlash(filepath.Join(dir, "tree"))
	for _, c := range []struct{ tool, args string }{
		{"code_grep", `{"pattern":"needle","path":"` + root + `"}`},
		{"code_glob", `{"pattern":"**/*.js","path":"` + root + `"}`},
		{"code_ls", `{"path":"` + root + `"}`},
	} {
		if out := runToolLoose(t, tools, c.tool, c.args); strings.Contains(out, "node_modules") {
			t.Errorf("%s walked into node_modules:\n%s", c.tool, out)
		}
	}
}

// The production assembly (Crush toolbox in LSPOnly mode + native overlay) must
// expose exactly the tool names the full Crush toolbox does, so nothing the
// model or the governance traits refer to goes missing.
func TestNativeAssemblyKeepsToolSet(t *testing.T) {
	names := func(tb coderuntime.Toolbox) string {
		var n []string
		for _, tl := range tb.Tools() {
			n = append(n, tl.Name)
		}
		sort.Strings(n)
		return strings.Join(n, ",")
	}
	dir := t.TempDir()
	full, err := ct.New(ct.Options{WorkDir: dir, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	lspOnly, err := ct.New(ct.Options{WorkDir: dir, DataDir: t.TempDir(), LSPOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer lspOnly.Close()

	base := Wrap(lspOnly)
	native := NativeFS(base, dir, nil)
	native = append(native, NativeSearch(SearchOptions{WorkDir: dir})...)
	if got, want := names(Overlay(base, native)), names(Wrap(full)); got != want {
		t.Fatalf("tool set changed\n got: %s\nwant: %s", got, want)
	}
}

// With ripgrep available, grep and glob must find the same files as the Go
// walker (the walker is the fallback when rg is missing).
func TestNativeSearchRgMatchesWalker(t *testing.T) {
	rg, err := exec.LookPath("rg")
	if err != nil {
		t.Skip("ripgrep not installed")
	}
	dir := t.TempDir()
	seedSearch(t, dir)
	root := filepath.ToSlash(filepath.Join(dir, "tree"))
	ctx := context.Background()
	with := &searcher{SearchOptions{WorkDir: dir, Rg: func() string { return rg }, GrepTimeout: 10 * time.Second, GlobTimeout: 10 * time.Second}}
	without := &searcher{SearchOptions{WorkDir: dir, Rg: func() string { return "" }, GrepTimeout: 10 * time.Second, GlobTimeout: 10 * time.Second}}

	files := func(out string) []string {
		var f []string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasSuffix(l, ":") || (!strings.HasPrefix(l, " ") && strings.Contains(l, "/") && !strings.HasPrefix(l, "Found")) {
				f = append(f, strings.TrimSuffix(l, ":"))
			}
		}
		sort.Strings(f)
		return f
	}
	g1 := with.grep(ctx, grepArgs{Pattern: "needle", Path: root}).Content
	g2 := without.grep(ctx, grepArgs{Pattern: "needle", Path: root}).Content
	if a, b := strings.Join(files(g1), "\n"), strings.Join(files(g2), "\n"); a != b || a == "" {
		t.Errorf("grep files differ\nrg:\n%s\nwalker:\n%s", a, b)
	}
	l1 := with.glob(ctx, globArgs{Pattern: "**/*.go", Path: root}).Content
	l2 := without.glob(ctx, globArgs{Pattern: "**/*.go", Path: root}).Content
	if a, b := strings.Join(files(l1), "\n"), strings.Join(files(l2), "\n"); a != b || a == "" {
		t.Errorf("glob files differ\nrg:\n%s\nwalker:\n%s", a, b)
	}
}

// assertParity runs scenario against the Crush tools and the native ones and
// requires identical model-visible output and identical files on disk. markers
// are strings the Crush transcript must contain, so the scenario cannot pass
// by only producing errors.
func assertParity(t *testing.T, seed func(*testing.T, string), scenario []step, markers []string) {
	t.Helper()
	wantT, wantD := runParity(t, false, seed, scenario)
	gotT, gotD := runParity(t, true, seed, scenario)

	all := strings.Join(wantT, "\n")
	for _, marker := range markers {
		if !strings.Contains(all, marker) {
			t.Errorf("scenario never produced %q", marker)
		}
	}

	if len(gotT) != len(wantT) {
		t.Fatalf("transcript length %d != %d", len(gotT), len(wantT))
	}
	names := scenario
	for i := range wantT {
		if gotT[i] != wantT[i] {
			t.Errorf("step %d (%s) differs\n--- crush ---\n%s\n--- native ---\n%s", i, names[i].tool, wantT[i], gotT[i])
		}
	}
	for name, want := range wantD {
		if got, ok := gotD[name]; !ok || got != want {
			t.Errorf("disk %s differs\n--- crush ---\n%q\n--- native ---\n%q", name, want, got)
		}
	}
	for name := range gotD {
		if _, ok := wantD[name]; !ok {
			t.Errorf("native created extra file %s", name)
		}
	}
}
