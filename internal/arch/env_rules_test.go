package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// envAllowed lists AGENTGO_* names that are a protocol with a child process,
// not runtime configuration.
var envAllowed = map[string]bool{
	"AGENTGO_ARGS_FILE": true, // handed to generated python tool wrappers
}

var envName = regexp.MustCompile(`^AGENTGO_[A-Z0-9_]+$`)

// TestAgentgoEnvIsOnlyNamedInConfig enforces F7: every AGENTGO_* setting is
// declared in internal/config and read through its typed handle. A bare
// "AGENTGO_X" string literal anywhere else means a code path reads (or sets) the
// environment itself, which splits the configuration surface again.
func TestAgentgoEnvIsOnlyNamedInConfig(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var bad []string

	walk := func(dir string) {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/config/") {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				name := strings.Trim(lit.Value, "\"`")
				if envName.MatchString(name) && !envAllowed[name] {
					bad = append(bad, rel+": \""+name+"\" - declare it in internal/config and read it through its handle")
				}
				return true
			})
			return nil
		})
	}
	walk("internal")
	walk("cmd")

	if len(bad) > 0 {
		sort.Strings(bad)
		t.Fatalf("AGENTGO_* named outside internal/config (%d):\n%s", len(bad), strings.Join(bad, "\n"))
	}
}
