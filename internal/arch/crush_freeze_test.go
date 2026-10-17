package arch_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// crushImportAllowlist is the complete set of AgentGo files that may import the
// vendored Crush code (docs/plan-crush-digestion.md, phase D0). It may only
// shrink: digestion removes entries, and a new import of third_party/crush
// anywhere else fails this test. Values are the allowed import path.
var crushImportAllowlist = map[string]string{
	"cmd/agentgo-tui/main.go": "agentgo/third_party/crush/pkg/embedtui",
}

func TestCrushImportsAreFrozen(t *testing.T) {
	seen := map[string]bool{}
	var bad []string
	scan := func(dir string) {
		for rel, imps := range importsOf(t, filepath.Join(repoRoot(), dir)) {
			for _, imp := range imps {
				if imp != "agentgo/third_party/crush" && !strings.HasPrefix(imp, "agentgo/third_party/crush/") {
					continue
				}
				seen[rel] = true
				// The Crush reference for the parity tests lives in
				// internal/codetools behind the crushparity build tag; it is not
				// part of any default build. Only that package may name it.
				if strings.HasPrefix(rel, "internal/codetools/") && imp == "agentgo/third_party/crush/pkg/codetools" {
					continue
				}
				if want, ok := crushImportAllowlist[rel]; !ok || want != imp {
					bad = append(bad, rel+" imports "+imp)
				}
			}
		}
	}
	scan("internal")
	scan("cmd")
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("new imports of vendored Crush are frozen (plan-crush-digestion D0):\n%s", strings.Join(bad, "\n"))
	}
	for rel := range crushImportAllowlist {
		if !seen[rel] {
			t.Errorf("%s no longer imports Crush; remove it from crushImportAllowlist (the list may only shrink)", rel)
		}
	}
}
