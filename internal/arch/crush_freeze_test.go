package arch_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Crush was digested into native packages and its source removed
// (docs/plan-crush-digestion.md, D7). Nothing may import it again, neither the
// old vendored path nor the upstream module.
func TestCrushIsNotImported(t *testing.T) {
	var bad []string
	for _, dir := range []string{"internal", "cmd", "pkg"} {
		for rel, imps := range importsOf(t, filepath.Join(repoRoot(), dir)) {
			for _, imp := range imps {
				if strings.Contains(imp, "third_party/crush") || strings.Contains(imp, "charmbracelet/crush") {
					bad = append(bad, rel+" imports "+imp)
				}
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("Crush has been removed; native packages replace it:\n%s", strings.Join(bad, "\n"))
	}
}
