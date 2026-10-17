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

// The native language-server tools against a real gopls (plan-crush-digestion
// D3); skipped when there is none. The comparison with Crush's tools lives in
// crush_parity_test.go behind the crushparity build tag.

func needGopls(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err == nil {
		return
	}
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err == nil {
		bin := filepath.Join(strings.TrimSpace(string(out)), "bin")
		if _, err := os.Stat(filepath.Join(bin, "gopls.exe")); err == nil {
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			return
		}
		if _, err := os.Stat(filepath.Join(bin, "gopls")); err == nil {
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			return
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
	// Candidate order follows mtime. Crush accepts the first candidate that
	// gives no error, and gopls answers a comment hit with an empty result, so
	// the order decides whether Crush lands on a real use. Pin it: b.go (real
	// uses) first. The native resolver skips empty answers and does not care.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, "a.go"), now.Add(-time.Minute), now.Add(-time.Minute))
	_ = os.Chtimes(filepath.Join(dir, "b.go"), now, now)
}

func lspTranscript(t *testing.T, box coderuntime.Toolbox, dir string) []string {
	t.Helper()
	byName := map[string]coderuntime.Tool{}
	for _, tl := range box.Tools() {
		byName[tl.Name] = tl
	}
	file := func(n string) string { return quote(filepath.Join(dir, n)) }
	steps := []struct{ tool, args string }{
		{"lsp_symbols", `{"file_path":` + file("a.go") + `}`},
		{"lsp_diagnostics", `{"file_path":` + file("b.go") + `}`},
		{"lsp_definition", `{"symbol":"Add"}`},
		{"lsp_references", `{"symbol":"Add"}`},
		{"lsp_call_hierarchy", `{"symbol":"Add","direction":"incoming"}`},
		{"lsp_call_hierarchy", `{"symbol":"Use","direction":"outgoing"}`},
		{"lsp_definition", `{"symbol":"NoSuchThingAnywhere"}`},
		{"lsp_symbols", `{"file_path":` + file("missing.go") + `}`},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var out []string
	for _, s := range steps {
		tl, ok := byName[s.tool]
		if !ok {
			t.Fatalf("tool %s missing", s.tool)
		}
		// Wait for the server the first call spawns; later calls reuse it.
		r, err := tl.Run(ctx, "s1", "c1", s.args)
		text := r.Content
		if err != nil {
			text = "ERR: " + err.Error()
		}
		low := strings.ToLower(text)
		for _, d := range []string{filepath.ToSlash(dir), dir} {
			low = strings.ReplaceAll(low, strings.ToLower(d), "<d>")
		}
		out = append(out, low)
	}
	return out
}

// lspMarkers are what a working gopls session must have produced somewhere in
// the transcript; a transcript of errors proves nothing.
var lspMarkers = []string{"function add", "error:", "found 1 definition", "reference(s)", "caller(s)", "callee(s)"}

func newNativeLSP(t *testing.T, dir string) *codelsp.Toolbox {
	t.Helper()
	nb, err := codelsp.New(codelsp.Options{
		WorkDir: dir,
		Search:  SymbolSearch(SearchOptions{WorkDir: dir, Rg: func() string { return "" }}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nb.Close)
	return nb
}

// No warm-up on purpose: the first call races the toolbox's background priming
// and must still get a real answer (Crush's tools failed here).
func TestNativeLSPToolsAgainstGopls(t *testing.T) {
	needGopls(t)
	dir := t.TempDir()
	seedGoModule(t, dir)
	got := lspTranscript(t, newNativeLSP(t, dir), dir)
	all := strings.Join(got, "\n")
	for _, m := range lspMarkers {
		if !strings.Contains(all, m) {
			t.Errorf("native transcript never produced %q:\n%s", m, all)
		}
	}
	if !strings.Contains(got[6], "not found") || !strings.Contains(got[7], "missing.go") {
		t.Errorf("error paths changed:\n%q\n%q", got[6], got[7])
	}

	// What Crush's tools answered for the same session (golden_test.go). Only
	// against the gopls it was recorded with: its wording moves between releases.
	want := loadGolden(t)
	if v := goplsVersion(); v != want.GoplsVersion {
		t.Logf("gopls %q differs from the recording's %q; skipping the Crush comparison", v, want.GoplsVersion)
		return
	}
	slash := func(s string) string { return strings.ReplaceAll(s, `\`, "/") }
	if len(got) != len(want.LSP.Transcript) {
		t.Fatalf("transcript length %d != recorded %d", len(got), len(want.LSP.Transcript))
	}
	for i := range got {
		if slash(got[i]) != slash(want.LSP.Transcript[i]) {
			t.Errorf("step %d differs from Crush\n--- crush ---\n%s\n--- native ---\n%s", i, want.LSP.Transcript[i], got[i])
		}
	}
}
