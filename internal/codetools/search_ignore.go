package codetools

// What the search tools leave out. Three layers, cheapest first:
//
//  1. folders that are never worth entering (version-control metadata,
//     dependency trees, caches, build output, this app's own state),
//  2. file names that are never worth showing (editor droppings, compiled
//     objects, lock files),
//  3. the project's own ignore files (.gitignore, .agentgoignore), read lazily
//     one directory at a time.

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// skipDirNames are matched against a single folder name, at any depth.
var skipDirNames = map[string]struct{}{}

func init() {
	for _, group := range [][]string{
		{".git", ".hg", ".svn", ".bzr"},                                        // version control
		{".idea", ".vscode", ".vs"},                                            // editors
		{"node_modules", "bower_components", "jspm_packages", "vendor"},        // dependencies
		{"__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox"}, // python tooling
		{".venv", "venv", ".gradle", ".next", ".nuxt", ".cache", ".tmp"},       // other tool state
		{"dist", "build", "target", "obj", "coverage"},                         // build output
		{".agentgo"}, // this app's own state
	} {
		for _, name := range group {
			skipDirNames[name] = struct{}{}
		}
	}
}

// skipFileGlobs are matched against a file's base name.
var skipFileGlobs = []string{
	"*.swp", "*.swo", "*~", ".DS_Store", "Thumbs.db",
	"*.o", "*.obj", "*.so", "*.dylib", "*.dll", "*.exe", "*.class", "*.pyc", "*.pyo",
	"*.log", "*.tmp", "*.temp",
	"Cargo.lock", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
}

// projectIgnoreFiles are read in every directory from the search root down.
var projectIgnoreFiles = []string{".gitignore", ".agentgoignore"}

func isNoiseDirName(name string) bool {
	_, ok := skipDirNames[name]
	return ok
}

func isNoiseFileName(name string) bool {
	for _, g := range skipFileGlobs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// inNoiseDir reports whether a folder between root and p is a skipped folder.
// It is how ripgrep's results get the same treatment as the walker's. Folders
// above root do not count: a workspace may itself live under one called "build".
func inNoiseDir(root, p string) bool {
	rel, err := filepath.Rel(root, filepath.Dir(p))
	if err != nil {
		return false
	}
	for _, part := range splitRel(rel) {
		if isNoiseDirName(part) {
			return true
		}
	}
	return false
}

// ignorer decides whether a path below root is skipped. Ignore files are parsed
// once per directory and the patterns accumulate from the root down.
type ignorer struct {
	root string
	// keepNoiseFiles stops file-name noise (lock files, objects) from being
	// hidden; folders and ignore files still apply.
	keepNoiseFiles bool

	mu      sync.Mutex
	rules   map[string][]gitignore.Pattern // dir -> patterns in force inside it
	matcher map[string]gitignore.Matcher
}

func newIgnorer(root string) *ignorer {
	return &ignorer{root: root, rules: map[string][]gitignore.Pattern{}, matcher: map[string]gitignore.Matcher{}}
}

func splitRel(rel string) []string {
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == "." {
		return nil
	}
	return strings.Split(rel, "/")
}

// rulesIn returns the patterns in force inside dir: its parent's plus its own
// ignore files. The caller holds mu.
func (ig *ignorer) rulesIn(dir string) []gitignore.Pattern {
	if r, ok := ig.rules[dir]; ok {
		return r
	}
	var inherited []gitignore.Pattern
	var domain []string
	if parent := filepath.Dir(dir); dir != ig.root && parent != dir {
		inherited = ig.rulesIn(parent)
		rel, _ := filepath.Rel(ig.root, dir)
		domain = splitRel(rel)
	}
	all := append([]gitignore.Pattern(nil), inherited...)
	for _, name := range projectIgnoreFiles {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
				all = append(all, gitignore.ParsePattern(line, domain))
			}
		}
	}
	ig.rules[dir] = all
	return all
}

func (ig *ignorer) matcherIn(dir string) gitignore.Matcher {
	ig.mu.Lock()
	defer ig.mu.Unlock()
	if m, ok := ig.matcher[dir]; ok {
		return m
	}
	m := gitignore.NewMatcher(ig.rulesIn(dir))
	ig.matcher[dir] = m
	return m
}

// ignored reports whether p is skipped. extra are base-name globs supplied by
// the caller (ls's "ignore" parameter).
func (ig *ignorer) ignored(p string, isDir bool, extra []string) bool {
	if p == ig.root {
		return false
	}
	base := filepath.Base(p)
	if isDir && isNoiseDirName(base) {
		return true
	}
	if !isDir && !ig.keepNoiseFiles && isNoiseFileName(base) {
		return true
	}
	for _, g := range extra {
		if ok, err := filepath.Match(g, base); err == nil && ok {
			return true
		}
	}
	rel, err := filepath.Rel(ig.root, p)
	if err != nil {
		rel = p
	}
	parts := splitRel(rel)
	if len(parts) == 0 {
		return false
	}
	return ig.matcherIn(filepath.Dir(p)).Match(parts, isDir)
}
