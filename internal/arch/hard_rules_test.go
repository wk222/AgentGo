package arch_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHardRules enforces the three dependency rules documented in ARCHITECTURE.md.
func TestHardRules(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var violations []string

	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		srcPkg := pathToInternalPkg(rel)
		if srcPkg == "" {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return nil
		}
		for _, imp := range f.Imports {
			target := strings.Trim(imp.Path.Value, `"`)
			if target == "agentgo/internal/bridge" && srcPkg != "bridge" {
				violations = append(violations, rel+": must not import bridge")
			}
			if target == "agentgo/internal/agent" {
				switch srcPkg {
				case "apps", "workflow", "tools":
					violations = append(violations, rel+": asset package must not import agent")
				case "agent":
					// ok
				}
			}
		}
		return nil
	})

	if len(violations) > 0 {
		t.Fatalf("hard rules (%d):\n%s", len(violations), strings.Join(violations, "\n"))
	}
}
