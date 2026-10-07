package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// concretePlugins are implementations that the runtime loads as plugins. Only
// the assembly (bridge) may name them; anything that imported one would make
// the plugin impossible to replace or leave out.
var concretePlugins = []string{
	"agentgo/internal/codetools",
	"agentgo/internal/crushproto",
	"agentgo/internal/engine/crushengine",
}

// contractOnlyStdlib packages are contracts or the host itself; they may not
// pull in anything of ours, so every plugin can depend on them freely.
var contractOnlyStdlib = []string{"plugin", "engine", "crushproto", "shellcmd", "hostlink"}

func repoRoot() string { return filepath.Join("..", "..") }

// importsOf returns, for every non-test Go file below dir, its import paths.
func importsOf(t *testing.T, dir string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			t.Errorf("parse %s: %v", path, perr)
			return nil
		}
		rel, _ := filepath.Rel(repoRoot(), path)
		for _, imp := range f.Imports {
			out[filepath.ToSlash(rel)] = append(out[filepath.ToSlash(rel)], strings.Trim(imp.Path.Value, `"`))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func underPkg(rel, pkg string) bool {
	return strings.HasPrefix(rel, "internal/"+pkg+"/")
}

// A concrete plugin is assembled by bridge and by nothing else, and plugins do
// not import each other: the host wires them together by service name.
func TestConcretePluginsAreOnlyReachedThroughTheAssembly(t *testing.T) {
	var bad []string
	for rel, imps := range importsOf(t, filepath.Join(repoRoot(), "internal")) {
		for _, imp := range imps {
			for _, plug := range concretePlugins {
				if imp != plug && !strings.HasPrefix(imp, plug+"/") {
					continue
				}
				self := strings.TrimPrefix(plug, "agentgo/")
				if strings.HasPrefix(rel, self+"/") || underPkg(rel, "bridge") {
					continue
				}
				bad = append(bad, rel+" imports "+imp)
			}
		}
	}
	// Entrypoints must also go through bridge (TestCMDOnlyImportsBridge covers
	// cmd/agentgo and cmd/llmtest; here the plugin packages specifically).
	for rel, imps := range importsOf(t, filepath.Join(repoRoot(), "cmd")) {
		for _, imp := range imps {
			for _, plug := range concretePlugins {
				if imp == plug || strings.HasPrefix(imp, plug+"/") {
					// Developer tools that exercise one plugin on its own.
					if !strings.HasPrefix(rel, "cmd/smoketest/") && !strings.HasPrefix(rel, "cmd/crushproto-probe/") {
						bad = append(bad, rel+" imports "+imp)
					}
				}
			}
		}
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Fatalf("concrete plugins must only be imported by internal/bridge (%d):\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

func TestHostAndContractPackagesImportOnlyTheStdlib(t *testing.T) {
	for _, pkg := range contractOnlyStdlib {
		for rel, imps := range importsOf(t, filepath.Join(repoRoot(), "internal", pkg)) {
			// Only the package itself: engine/crushengine is an implementation of
			// the engine contract and rightly imports it.
			if filepath.ToSlash(filepath.Dir(rel)) != "internal/"+pkg {
				continue
			}
			for _, imp := range imps {
				if strings.HasPrefix(imp, "agentgo/") {
					t.Errorf("%s imports %s; internal/%s must depend on the standard library only", rel, imp, pkg)
				}
			}
		}
	}
}

// appServiceCoupled lists the plugin constructors that still need the
// application facade, with the reason. It may only shrink: an entry that no
// longer references AppService must be removed, and a new entry needs review.
var appServiceCoupled = map[string]string{
	"crushprotoPlugin": "the Crush protocol adapter drives sessions, runs, approvals, LSP and model views through the application (declared as the \"brain\" service)",
	"hostlinkPlugin":   "the host endpoint serves that same Crush protocol adapter to attached terminals (declared as the \"brain\" service)",
}

func mentionsIdent(n ast.Node, name string) (token.Pos, bool) {
	var at token.Pos
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if id, ok := x.(*ast.Ident); ok && id.Name == name && !found {
			at, found = id.Pos(), true
		}
		return !found
	})
	return at, found
}

func returnsPlugin(fn *ast.FuncDecl) bool {
	if fn.Type.Results == nil {
		return false
	}
	for _, r := range fn.Type.Results.List {
		t := r.Type
		if arr, ok := t.(*ast.ArrayType); ok {
			t = arr.Elt
		}
		if sel, ok := t.(*ast.SelectorExpr); ok && sel.Sel.Name == "Plugin" {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "plugin" {
				return true
			}
		}
	}
	return false
}

// Every function that builds a plugin in bridge takes what it needs from the
// Runtime or from the host. Closing over the AppService is how plugins used to
// depend on it without anyone seeing; it is rejected here by name.
func TestPluginConstructorsDoNotDependOnAppService(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(), "internal", "bridge", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := map[string]bool{} // constructors found
	used := map[string]bool{} // allowlisted ones that still reference AppService
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			continue
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !returnsPlugin(fn) {
				continue
			}
			name := fn.Name.Name
			seen[name] = true
			pos, bad := mentionsIdent(fn, "AppService")
			if !bad {
				continue
			}
			if _, ok := appServiceCoupled[name]; ok {
				used[name] = true
				continue
			}
			t.Errorf("%s: %s references AppService; take a *Runtime or Inject a service from the host instead",
				fset.Position(pos), name)
		}
	}
	for name := range appServiceCoupled {
		if !seen[name] {
			t.Errorf("allowlist entry %q does not name a plugin constructor any more; remove it", name)
		} else if !used[name] {
			t.Errorf("%q no longer references AppService; remove it from appServiceCoupled", name)
		}
	}
	for _, want := range []string{"einoPlugin", "crushPlugin", "crushprotoPlugin", "hostlinkPlugin", "codetoolsPlugin", "dataPlugins", "agentPlugins", "featurePlugins"} {
		if !seen[want] {
			t.Errorf("constructor %s was not found; the check is not looking at the right code", want)
		}
	}
}

// The runtime's assembly files were written to go through the plugin host. A
// reference to AppService there would bring back the hidden dependency on the
// desktop-facing facade, so it is rejected by name (comments are not code).
func TestRuntimeAssemblyDoesNotDependOnAppService(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(repoRoot(), "internal", "bridge", "runtime_plugins_*.go"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join(repoRoot(), "internal", "bridge", "runtime_boot.go"))
	checked := 0
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("parse %s: %v", path, err)
			continue
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == "AppService" {
				t.Errorf("%s: references AppService; assembly code must depend on Runtime and plugin services only",
					fset.Position(id.Pos()))
			}
			return true
		})
	}
	if checked < 4 {
		t.Fatalf("expected at least 4 assembly files, found %d: the test is not looking in the right place", checked)
	}
}
