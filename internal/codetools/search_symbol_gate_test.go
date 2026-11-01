package codetools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/coderuntime"
)

// lsp_definition / lsp_references / lsp_call_hierarchy resolve their search
// root from the model's path argument (codelsp.searchRoot joins relative ones
// onto the workspace) and then call SymbolSearch, which greps that tree. That
// path bypassed the gate grep and glob have, so a symbol query read outside the
// workspace with nobody asked.
func TestSymbolSearchGatesOutsideWorkDir(t *testing.T) {
	dir := relativeWorkdir(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.go"), []byte("package x\n// needletoken\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var asked []string
	deny := func(_ context.Context, r coderuntime.FSRequest) bool {
		asked = append(asked, r.Tool+" "+r.Path)
		return false
	}
	search := SymbolSearch(SearchOptions{WorkDir: dir, Approve: deny, Rg: func() string { return "" }})

	hits, err := search(context.Background(), `needletoken`, outside, 100)
	if err == nil {
		t.Fatalf("symbol search outside the workspace was allowed without approval: %v", hits)
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(asked) != 1 {
		t.Fatalf("the gate was not consulted exactly once: %v", asked)
	}

	// Inside the workspace nothing is asked and the symbol is found.
	asked = nil
	hits, err = search(context.Background(), `needle`, dir, 100)
	if err != nil {
		t.Fatalf("symbol search inside the workspace: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("symbol search inside the workspace found nothing")
	}
	if len(asked) != 0 {
		t.Fatalf("symbol search asked about a path inside the workspace: %v", asked)
	}
}
