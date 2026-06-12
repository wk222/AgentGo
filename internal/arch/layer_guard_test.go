package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/arch"
)

func TestEveryInternalPackageRegistered(t *testing.T) {
	root := filepath.Join("..", "..", "internal")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "arch" {
			continue
		}
		if _, ok := arch.PackageLayer[name]; !ok {
			t.Errorf("package internal/%s is not registered in arch.PackageLayer", name)
		}
	}
}

func TestNoUpwardInternalImports(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var violations []string

	_ = filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		srcPkg := pathToInternalPkg(rel)
		if srcPkg == "" || srcPkg == "arch" {
			return nil
		}
		srcLayer, ok := arch.PackageLayer[srcPkg]
		if !ok {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("parse %s: %v", rel, err)
			return nil
		}
		for _, imp := range f.Imports {
			target := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(target, "agentgo/internal/") {
				continue
			}
			tgtPkg := strings.TrimPrefix(target, "agentgo/internal/")
			if tgtPkg == "arch" {
				continue
			}
			tgtLayer, ok := arch.PackageLayer[tgtPkg]
			if !ok {
				violations = append(violations, rel+": imports unregistered "+target)
				continue
			}
			if tgtLayer > srcLayer {
				violations = append(violations,
					rel+" ["+arch.LayerLabel(srcLayer)+"] imports "+target+" ["+arch.LayerLabel(tgtLayer)+"]")
			}
		}
		return nil
	})

	if len(violations) > 0 {
		t.Fatalf("upward imports (%d):\n%s", len(violations), strings.Join(violations, "\n"))
	}
}

func TestArchPackageOnlyStdlib(t *testing.T) {
	root := filepath.Join(".")
	fset := token.NewFileSet()
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			imp, ok := n.(*ast.ImportSpec)
			if !ok {
				return true
			}
			p := strings.Trim(imp.Path.Value, `"`)
			if strings.HasPrefix(p, "agentgo/internal/") && p != "agentgo/internal/arch" {
				t.Errorf("arch must not import %s (%s)", p, path)
			}
			return true
		})
		return nil
	})
}

func TestCMDOnlyImportsBridge(t *testing.T) {
	// Production entrypoints only; cmd/smoketest is allowed to reach deeper for CI.
	for _, sub := range []string{"agentgo", "llmtest"} {
		root := filepath.Join("..", "..", "cmd", sub)
		fset := token.NewFileSet()
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return nil
			}
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if strings.HasPrefix(p, "agentgo/internal/") && p != "agentgo/internal/bridge" {
					t.Errorf("%s: cmd/%s may only import bridge, found %s", path, sub, p)
				}
			}
			return nil
		})
	}
}

func pathToInternalPkg(rel string) string {
	// internal/apps/foo.go -> apps
	parts := strings.Split(rel, "/")
	if len(parts) < 2 || parts[0] != "internal" {
		return ""
	}
	return parts[1]
}
