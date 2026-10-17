package codelsp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
		t.Fatal("malformed lsp.json must be reported")
	}
}

func TestHandlesFiletype(t *testing.T) {
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
		if got := handlesFiletype(c.types, c.path); got != c.want {
			t.Errorf("handlesFiletype(%v, %q) = %v, want %v", c.types, c.path, got, c.want)
		}
	}
}

func TestHasRootMarkers(t *testing.T) {
	dir := t.TempDir()
	if !hasRootMarkers(dir, nil) {
		t.Fatal("no markers means always applicable")
	}
	if hasRootMarkers(dir, []string{"go.mod", "*.csproj"}) {
		t.Fatal("empty dir has no markers")
	}
	if err := os.WriteFile(filepath.Join(dir, "x.csproj"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !hasRootMarkers(dir, []string{"go.mod", "*.csproj"}) {
		t.Fatal("glob marker should match")
	}
}

func TestManagerOverridesAndDisable(t *testing.T) {
	m := NewManager(ManagerOptions{
		WorkDir: t.TempDir(),
		Overrides: Overrides{
			"gopls": {Disabled: true},
			"mine":  {Command: "mine-lsp", FileTypes: []string{".mine"}},
		},
		NoAuto: true,
	})
	if _, ok := m.servers["mine"]; !ok || !m.user["mine"] {
		t.Fatal("user server should be added and flagged as user-configured")
	}
	if spec, ok := m.servers["gopls"]; ok && !spec.Disabled {
		t.Fatal("gopls should be disabled by the override")
	}
}

func TestIsNoIdentifier(t *testing.T) {
	if !isNoIdentifier(errors.New("no identifier found")) {
		t.Fatal("should match gopls 'no identifier found'")
	}
	if isNoIdentifier(errors.New("connection reset")) {
		t.Fatal("unrelated errors must not be treated as non-identifier")
	}
}

func TestClientStartedGate(t *testing.T) {
	c := &Client{started: make(chan struct{})}
	select {
	case <-c.started:
		t.Fatal("must not be started before markStarted")
	default:
	}
	c.markStarted()
	c.markStarted() // idempotent
	<-c.started
}
