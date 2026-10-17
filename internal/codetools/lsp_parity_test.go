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
	ct "agentgo/third_party/crush/pkg/codetools"
)

// Parity (plan-crush-digestion D3): the native language-server tools must give
// the model what the Crush ones did. It needs a real gopls, so it skips when
// there is none.

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

func waitGoplsReady(t *testing.T, box coderuntime.Toolbox, file string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		box.StartLSP(context.Background(), file)
		if st, ok := box.LSPStates()["gopls"]; ok && st.State == int(codelsp.StateReady) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("gopls never became ready")
}

func TestNativeLSPParityWithCrush(t *testing.T) {
	needGopls(t)

	crushDir := t.TempDir()
	seedGoModule(t, crushDir)
	tb, err := ct.New(ct.Options{WorkDir: crushDir, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer tb.Close()
	// Two Crush quirks the baseline has to be shielded from; the native side
	// gets neither help. Its symbol tools grep from the process's cwd, not the
	// workspace; and a call that arrives while a server is still starting (the
	// toolbox primes servers in the background) queries it before initialize.
	t.Chdir(crushDir)
	crushBox := Wrap(tb)
	waitGoplsReady(t, crushBox, filepath.Join(crushDir, "a.go"))
	want := lspTranscript(t, crushBox, crushDir)

	nativeDir := t.TempDir()
	seedGoModule(t, nativeDir)
	nb, err := codelsp.New(codelsp.Options{
		WorkDir: nativeDir,
		Search:  SymbolSearch(SearchOptions{WorkDir: nativeDir, Rg: func() string { return "" }}),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer nb.Close()
	got := lspTranscript(t, nb, nativeDir)

	// gopls must really have answered: a transcript of errors proves nothing.
	all := strings.Join(want, "\n")
	for _, marker := range []string{"function add", "error:", "found 1 definition", "reference(s)", "caller(s)", "callee(s)"} {
		if !strings.Contains(all, marker) {
			t.Errorf("crush transcript never produced %q:\n%s", marker, all)
		}
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("step %d differs\n--- crush ---\n%s\n--- native ---\n%s", i, want[i], got[i])
		}
	}
}
