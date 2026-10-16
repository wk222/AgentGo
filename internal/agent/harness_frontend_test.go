package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// frontend/src/harness.ts keeps a copy of the presets as the offline / browser-preview
// fallback. The backend is the source of truth; this test fails when the copy drifts.
func TestFrontendFallbackHarnessesMatchBackend(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "harness.ts"))
	if err != nil {
		t.Skipf("frontend sources not available: %v", err)
	}
	re := regexp.MustCompile(`\{\s*id:\s*'([^']+)',\s*name:\s*'([^']+)',\s*label:\s*'([^']+)',\s*desc:\s*'([^']+)',\s*profile:\s*'([^']+)',\s*canvas:\s*'([^']+)'\s*\}`)
	found := re.FindAllStringSubmatch(string(raw), -1)
	want := Harnesses()
	if len(found) != len(want) {
		t.Fatalf("harness.ts lists %d presets, backend has %d", len(found), len(want))
	}
	for i, m := range found {
		h := want[i]
		got := Harness{ID: m[1], Name: m[2], Label: m[3], Desc: m[4], Profile: ModeProfile(m[5]), Canvas: ExecutionCanvas(m[6])}
		if got != h {
			t.Errorf("preset %d differs:\n frontend: %+v\n backend:  %+v", i, got, h)
		}
	}
}
