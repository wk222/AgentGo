package codelsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func TestLoadOverrides(t *testing.T) {
	dir := t.TempDir()
	if o, err := LoadOverrides(dir); err != nil || o != nil {
		t.Fatalf("missing file: got %v, %v", o, err)
	}
	body := `{"mylsp":{"command":"my-lsp","filetypes":["foo"]},"gopls":{"disabled":true}}`
	if err := os.WriteFile(filepath.Join(dir, "lsp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	o, err := LoadOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	if o["mylsp"].Command != "my-lsp" || !o["gopls"].Disabled {
		t.Fatalf("overrides not parsed: %+v", o)
	}
	if err := os.WriteFile(filepath.Join(dir, "lsp.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOverrides(dir); err == nil {
		t.Fatal("a malformed lsp.json must be reported")
	}
}

func TestMatchesFileType(t *testing.T) {
	cases := []struct {
		types []string
		path  string
		want  bool
	}{
		{nil, "x.anything", true},
		{[]string{".go"}, "/p/a.go", true},
		{[]string{"go"}, "/p/A.GO", true},
		{[]string{".go"}, "/p/a.rs", false},
		{[]string{".tsx"}, "/p/a.tsx", true},
	}
	for _, c := range cases {
		if got := matchesFileType(c.types, c.path); got != c.want {
			t.Errorf("matchesFileType(%v, %q) = %v, want %v", c.types, c.path, got, c.want)
		}
	}
}

func TestHasRootMarker(t *testing.T) {
	dir := t.TempDir()
	if !hasRootMarker(dir, nil) {
		t.Fatal("no markers means the server always applies")
	}
	if hasRootMarker(dir, []string{"go.mod", "*.csproj"}) {
		t.Fatal("an empty folder has no markers")
	}
	if err := os.WriteFile(filepath.Join(dir, "x.csproj"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasRootMarker(dir, []string{"go.mod", "*.csproj"}) {
		t.Fatal("a glob marker should match")
	}
}

func TestCatalogOverridesAddAndDisable(t *testing.T) {
	cat := buildCatalog(Overrides{
		"gopls": {Disabled: true},
		"mine":  {Command: "mine-lsp", FileTypes: []string{".mine"}},
	})
	if _, ok := cat.specs["mine"]; !ok || !cat.user["mine"] {
		t.Fatal("a user server should be added and flagged as user-configured")
	}
	if _, ok := cat.specs["gopls"]; ok {
		t.Fatal("gopls should be removed by the override")
	}
}

func TestCatalogAcceptsTheCommandWhereTheNameGoes(t *testing.T) {
	cat := buildCatalog(Overrides{"typescript-language-server": {Command: "tsls", FileTypes: []string{".ts"}}})
	// Whatever bundled server uses that command is the one replaced; if none does,
	// the key is simply a new server. Either way exactly one entry carries "tsls".
	n := 0
	for _, s := range cat.specs {
		if s.Command == "tsls" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want one replaced/added server, got %d", n)
	}
}

func TestIsNoIdentifier(t *testing.T) {
	if !isNoIdentifier(errors.New("no identifier found")) {
		t.Fatal("gopls' 'no identifier found' must be recognised")
	}
	if isNoIdentifier(errors.New("connection reset")) || isNoIdentifier(nil) {
		t.Fatal("unrelated errors must not be mistaken for a non-identifier")
	}
}

func TestNameOffset(t *testing.T) {
	for sym, want := range map[string]int{"Add": 0, "pkg.Add": 4, `a\b\Add`: 4, "Type::method": 6, "a.b.C": 4} {
		if got := nameOffset(sym); got != want {
			t.Errorf("nameOffset(%q) = %d, want %d", sym, got, want)
		}
	}
}

func TestSessionSettledGate(t *testing.T) {
	s := &session{settled: make(chan struct{})}
	select {
	case <-s.settled:
		t.Fatal("must not be settled before markSettled")
	default:
	}
	s.markSettled()
	s.markSettled() // idempotent
	<-s.settled

	waiter := &session{settled: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { waiter.awaitSettled(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("awaitSettled must return when its context ends")
	}
}

func TestAwaitDiagnosticsReturnsEarlyWhenServerStaysSilent(t *testing.T) {
	s := &session{}
	start := time.Now()
	s.awaitDiagnostics(context.Background(), 10*time.Second)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("waited %v for a server that never speaks", took)
	}
}

func TestAwaitDiagnosticsWaitsForTheServerToSettle(t *testing.T) {
	s := &session{}
	go func() {
		for i := 0; i < 4; i++ {
			time.Sleep(150 * time.Millisecond)
			s.diags.replace("file:///a.go", []protocol.Diagnostic{{Message: "x"}})
		}
	}()
	start := time.Now()
	s.awaitDiagnostics(context.Background(), 10*time.Second)
	if took := time.Since(start); took < 600*time.Millisecond {
		t.Fatalf("returned after %v, before the burst of updates finished", took)
	}
}

// reportFor builds a toolset whose single fake server holds the given diagnostics.
func reportFor(t *testing.T, root string, byFile map[string][]protocol.Diagnostic) *toolset {
	t.Helper()
	sv := newSupervisor(supervisorOptions{root: root, noAuto: true})
	s := &session{name: "fakels", root: root}
	for path, list := range byFile {
		s.diags.replace(protocol.URIFromPath(path), list)
	}
	sv.live["fakels"] = s
	return &toolset{sv: sv, root: root}
}

func TestReportPutsTheRequestedFileFirstAndErrorsBeforeWarnings(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	at := func(line uint32) protocol.Range {
		return protocol.Range{Start: protocol.Position{Line: line, Character: 4}}
	}
	ts := reportFor(t, root, map[string][]protocol.Diagnostic{
		a: {
			{Range: at(9), Severity: protocol.SeverityWarning, Message: "unused value", Source: "vet"},
			{Range: at(2), Severity: protocol.SeverityError, Message: "undefined: x", Source: "compiler", Code: "UndeclaredName"},
		},
		b: {{Range: at(0), Severity: protocol.SeverityError, Message: "syntax error"}},
	})

	out := ts.report(a)
	fileAt, otherAt := strings.Index(out, "Problems in "+a), strings.Index(out, "Problems in other files")
	if fileAt < 0 || otherAt < fileAt {
		t.Fatalf("the requested file must come first:\n%s", out)
	}
	if e, w := strings.Index(out, "undefined: x"), strings.Index(out, "unused value"); e < 0 || w < e {
		t.Fatalf("errors must precede warnings:\n%s", out)
	}
	for _, want := range []string{"1 error, 1 warning", "3:5", "UndeclaredName", b + ":1:5"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}

	if all := ts.report(""); !strings.Contains(all, "Problems in the project") || strings.Contains(all, "other files") {
		t.Fatalf("a project report has a single section:\n%s", all)
	}
}

func TestReportCapsEachSectionAndIsEmptyWhenNothingWasReported(t *testing.T) {
	root := t.TempDir()
	if got := reportFor(t, root, nil).report(""); got != "" {
		t.Fatalf("no diagnostics must give an empty report, got %q", got)
	}

	many := make([]protocol.Diagnostic, 0, 25)
	for i := range 25 {
		many = append(many, protocol.Diagnostic{Range: protocol.Range{Start: protocol.Position{Line: uint32(i)}}, Severity: protocol.SeverityError, Message: "boom"})
	}
	out := reportFor(t, root, map[string][]protocol.Diagnostic{filepath.Join(root, "big.go"): many}).report("")
	if !strings.Contains(out, "15 more not shown") || strings.Count(out, "boom") != listedPerSection {
		t.Fatalf("a long section must be capped at %d lines:\n%s", listedPerSection, out)
	}
}

func TestRenderReferencesGroupsByFileAndShowsTheSourceLine(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.go")
	if err := os.WriteFile(a, []byte("package m\n\nfunc Use() int { return Add(1, 2) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	loc := func(line, col uint32) protocol.Location {
		return protocol.Location{URI: protocol.URIFromPath(a), Range: protocol.Range{Start: protocol.Position{Line: line, Character: col}}}
	}
	// A duplicate and an out-of-order entry: the output must be sorted and unique.
	out := renderReferences("Add", []protocol.Location{loc(2, 22), loc(2, 22), loc(0, 0)})
	if !strings.Contains(out, "2 references in 1 file") {
		t.Fatalf("count line wrong:\n%s", out)
	}
	if !strings.Contains(out, "3:23  func Use() int { return Add(1, 2) }") {
		t.Fatalf("source line missing:\n%s", out)
	}
	if strings.Index(out, "1:1") > strings.Index(out, "3:23") {
		t.Fatalf("references must be in source order:\n%s", out)
	}
}

func TestRenderDefinitionsMarksTheDefiningLine(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a.go")
	var src strings.Builder
	for i := 1; i <= 8; i++ {
		src.WriteString("line\n")
	}
	if err := os.WriteFile(a, []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	out := renderDefinitions("X", []protocol.Location{{URI: protocol.URIFromPath(a), Range: protocol.Range{Start: protocol.Position{Line: 4}}}})
	if !strings.Contains(out, a+":5") || !strings.Contains(out, "->    5 | line") || !strings.Contains(out, "      3 | line") {
		t.Fatalf("definition context wrong:\n%s", out)
	}
}
