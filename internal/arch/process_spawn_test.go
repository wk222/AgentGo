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

// consoleExempt lists the files whose child processes run in a console the user opened themselves
// (command-line tools and the evaluation harness): they never need a hidden window.
var consoleExempt = map[string]bool{
	"internal/evaluation/verify.go": true,
}

// localConsoleExempt merges machine-local exemptions (tools from local-only
// packages that are not part of this repository) from
// process_spawn_exempt.local.txt, one repo-relative path per line. That file
// is git-ignored; a fresh clone simply has no extra exemptions.
func localConsoleExempt() map[string]bool {
	out := map[string]bool{}
	for k, v := range consoleExempt {
		out[k] = v
	}
	if b, err := os.ReadFile("process_spawn_exempt.local.txt"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				out[line] = true
			}
		}
	}
	return out
}

// TestEveryProcessSpawnHidesItsConsoleWindow keeps the desktop build from flashing console windows.
//
// The desktop (windowsgui) build has no console. A console program it starts without
// CREATE_NO_WINDOW (rg, git, python, uv, an MCP adapter ...) gets a console window of its own that
// flashes on screen and can steal the focus from the user's work. processutil.HideWindow (or
// workspace.HideExecWindow) sets the flag; any file that starts a process through os/exec must use one.
func TestEveryProcessSpawnHidesItsConsoleWindow(t *testing.T) {
	root := filepath.Join("..", "..")
	fset := token.NewFileSet()
	exempt := localConsoleExempt()
	var offenders []string
	for _, top := range []string{"internal", "pkg"} {
		_ = filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if exempt[rel] || notWindowsFile(rel) {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				return nil
			}
			spawns, hides := false, false
			ast.Inspect(f, func(n ast.Node) bool {
				switch v := n.(type) {
				case *ast.CallExpr:
					switch fn := v.Fun.(type) {
					case *ast.SelectorExpr:
						if x, ok := fn.X.(*ast.Ident); ok && x.Name == "exec" && (fn.Sel.Name == "Command" || fn.Sel.Name == "CommandContext") {
							spawns = true
						}
						// shellcmd.StartTree sets HideWindow on the command it starts.
						if fn.Sel.Name == "HideWindow" || fn.Sel.Name == "HideExecWindow" || fn.Sel.Name == "StartTree" {
							hides = true
						}
					case *ast.Ident:
						if fn.Name == "HideExecWindow" || fn.Name == "hideWindow" {
							hides = true
						}
					}
				case *ast.SelectorExpr:
					if v.Sel.Name == "SysProcAttr" {
						hides = true // the file configures the process attributes itself
					}
				}
				return true
			})
			if spawns && !hides {
				offenders = append(offenders, rel)
			}
			return nil
		})
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("these files start processes without hiding the console window (call processutil.HideWindow(cmd)):\n%s",
			strings.Join(offenders, "\n"))
	}
}

// notWindowsFile reports files that only build on other systems, where a console window is no issue.
func notWindowsFile(rel string) bool {
	base := strings.TrimSuffix(filepath.Base(rel), ".go")
	for _, suffix := range []string{"_other", "_unix", "_linux", "_darwin", "_posix"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}
