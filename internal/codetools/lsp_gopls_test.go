package codetools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentgo/internal/codelsp"
	"agentgo/internal/coderuntime"
)

// The language-server tools against a real gopls; skipped when there is none.

func needGopls(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err == nil {
		return
	}
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err == nil {
		bin := filepath.Join(strings.TrimSpace(string(out)), "bin")
		for _, name := range []string{"gopls.exe", "gopls"} {
			if _, err := os.Stat(filepath.Join(bin, name)); err == nil {
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				return
			}
		}
	}
	t.Skip("gopls not installed")
}

func seedGoModule(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.21\n",
		"a.go": "package m\n\n// Add adds two numbers.\nfunc Add(a, b int) int { return a + b }\n\n" +
			"type Greeter struct{ Name string }\n\nfunc (g Greeter) Hello() string { return \"hi \" + g.Name }\n",
		"b.go": "package m\n\nfunc Use() int { return Add(1, 2) }\n\nfunc Again() int { return Add(3, 4) }\n\nfunc Bad() int { return \"x\" }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The doc comment in a.go mentions Add too; make b.go (the real uses) the
	// newest so it is the first candidate. The resolver must cope either way.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, "a.go"), now.Add(-time.Minute), now.Add(-time.Minute))
	_ = os.Chtimes(filepath.Join(dir, "b.go"), now, now)
}

func newLSP(t *testing.T, dir string) *codelsp.Toolbox {
	t.Helper()
	tb, err := codelsp.New(codelsp.Options{
		WorkDir: dir,
		Search:  SymbolSearch(SearchOptions{WorkDir: dir, Rg: func() string { return "" }}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tb.Close)
	return tb
}

// No warm-up on purpose: the first call races the toolbox's background priming
// and must still get a real answer.
func TestLSPToolsAgainstGopls(t *testing.T) {
	needGopls(t)
	dir := t.TempDir()
	seedGoModule(t, dir)
	byName := map[string]coderuntime.Tool{}
	for _, tl := range newLSP(t, dir).Tools() {
		byName[tl.Name] = tl
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	call := func(tool, args string) string {
		t.Helper()
		tl, ok := byName[tool]
		if !ok {
			t.Fatalf("tool %s missing", tool)
		}
		r, err := tl.Run(ctx, "s1", "c1", args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		return strings.ToLower(strings.ReplaceAll(r.Content, `\`, "/"))
	}
	file := func(n string) string { return quote(filepath.ToSlash(filepath.Join(dir, n))) }

	cases := []struct {
		name, tool, args string
		want             []string
	}{
		{"outline", "lsp_symbols", `{"file_path":` + file("a.go") + `}`, []string{"function add", "struct greeter", "method (greeter).hello"}},
		{"diagnostics", "lsp_diagnostics", `{"file_path":` + file("b.go") + `}`, []string{"problems in", "1 error", "error"}},
		{"definition", "lsp_definition", `{"symbol":"Add"}`, []string{"is defined in 1 place", "a.go:4", "-> "}},
		{"references", "lsp_references", `{"symbol":"Add"}`, []string{"references in", "b.go", "return add(1, 2)"}},
		{"callers", "lsp_call_hierarchy", `{"symbol":"Add","direction":"incoming"}`, []string{"callers of add", "use", "again"}},
		{"callees", "lsp_call_hierarchy", `{"symbol":"Use","direction":"outgoing"}`, []string{"calls made by use", "add"}},
		{"unknown symbol", "lsp_definition", `{"symbol":"NoSuchThingAnywhere"}`, []string{"could not find"}},
		{"missing file", "lsp_symbols", `{"file_path":` + file("missing.go") + `}`, []string{"missing.go"}},
	}
	for _, c := range cases {
		out := call(c.tool, c.args)
		for _, w := range c.want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: output lacks %q:\n%s", c.name, w, out)
			}
		}
	}
}
