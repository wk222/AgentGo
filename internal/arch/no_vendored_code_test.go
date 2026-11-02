package arch_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The code tools and the language-server layer are AgentGo's own code. Nothing
// may import a vendored copy under third_party/ or another agent's module.
func TestNoVendoredAgentCodeIsImported(t *testing.T) {
	var bad []string
	for _, dir := range []string{"internal", "cmd", "pkg"} {
		for rel, imps := range importsOf(t, filepath.Join(repoRoot(), dir)) {
			for _, imp := range imps {
				if strings.Contains(imp, "third_party/") || strings.Contains(imp, "charmbracelet/crush") {
					bad = append(bad, rel+" imports "+imp)
				}
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("vendored agent code must not come back:\n%s", strings.Join(bad, "\n"))
	}
}
