package codetools

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The Crush tools this package replaced are no longer in the tree. What they
// answered for the parity scenarios was recorded once, from the real
// implementation, in testdata/crush_golden.json; the native tools are held to
// those recordings. Nothing here is regenerated from the native side: a
// changed answer must be a deliberate edit of the recording, in review.

const goldenPath = "testdata/crush_golden.json"

type goldenRun struct {
	Transcript []string          `json:"transcript"`
	Disk       map[string]string `json:"disk,omitempty"`
}

type goldenFile struct {
	// GoplsVersion is the first line of `gopls version` when the LSP part was
	// recorded; diagnostics wording can move between releases, so the LSP
	// comparison only runs against the same version.
	GoplsVersion string    `json:"gopls_version"`
	FS           goldenRun `json:"fs"`
	Search       goldenRun `json:"search"`
	LSP          goldenRun `json:"lsp"`
}

func loadGolden(t *testing.T) goldenFile {
	t.Helper()
	b, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("golden recording missing: %v", err)
	}
	var g goldenFile
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

func goplsVersion() string {
	out, err := exec.Command("gopls", "version").Output()
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}
