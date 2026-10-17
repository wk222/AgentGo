package codetools

// Ignore rules shared by the native grep / glob / ls tools. The rule set
// (always-ignored directory names, common build/lock patterns, hierarchical
// .gitignore files) is derived from Crush's fsext package (licenses/crush/NOTICE.md,
// FSL-1.1-MIT) without its global-excludes and Crush-config lookups.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

// fastIgnoreDirs are directory names that are never searched or listed.
var fastIgnoreDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true, ".bzr": true,
	".vscode": true, ".idea": true, "node_modules": true,
	"__pycache__": true, ".pytest_cache": true, ".cache": true, ".tmp": true,
	".Trash": true, ".Spotlight-V100": true, ".fseventsd": true,
	".crush": true, ".agentgo": true, "OrbStack": true, ".local": true, ".share": true,
}

var commonIgnoreLines = []string{
	"*.swp", "*.swo", "*~", ".DS_Store", "Thumbs.db",
	"target", "build", "dist", "out", "bin", "obj",
	"*.o", "*.so", "*.dylib", "*.dll", "*.exe",
	"*.log", "*.tmp", "*.temp",
	"*.pyc", "*.pyo", "vendor",
	"Cargo.lock", "package-lock.json", "yarn.lock", "pnpm-lock.yaml",
}

// ignoreFileNames are read in every directory from the root down.
var ignoreFileNames = []string{".gitignore", ".agentgoignore", ".crushignore"}

func parseIgnoreLines(lines []string, domain []string) []gitignore.Pattern {
	var out []gitignore.Pattern
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, gitignore.ParsePattern(line, domain))
	}
	return out
}

var commonIgnorePatterns = sync.OnceValue(func() []gitignore.Pattern {
	return parseIgnoreLines(commonIgnoreLines, nil)
})

// ignorer answers "is this path ignored?" for paths below root, reading ignore
// files lazily and caching the combined matcher per directory.
type ignorer struct {
	root string

	mu       sync.Mutex
	dirPats  map[string][]gitignore.Pattern
	matchers map[string]gitignore.Matcher
}

func newIgnorer(root string) *ignorer {
	return &ignorer{root: root, dirPats: map[string][]gitignore.Pattern{}, matchers: map[string]gitignore.Matcher{}}
}

func pathComponents(rel string) []string {
	rel = filepath.ToSlash(rel)
	if rel == "" || rel == "." {
		return nil
	}
	return strings.Split(rel, "/")
}

// patternsIn parses the ignore files of one directory. Caller holds mu.
func (ig *ignorer) patternsIn(dir string) []gitignore.Pattern {
	if p, ok := ig.dirPats[dir]; ok {
		return p
	}
	var domain []string
	if rel, _ := filepath.Rel(ig.root, dir); rel != "" && rel != "." {
		domain = pathComponents(rel)
	}
	var all []gitignore.Pattern
	for _, name := range ignoreFileNames {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			all = append(all, parseIgnoreLines(strings.Split(string(b), "\n"), domain)...)
		}
	}
	ig.dirPats[dir] = all
	return all
}

// matcherFor combines the common patterns with every ignore file from the
// root to dir.
func (ig *ignorer) matcherFor(dir string) gitignore.Matcher {
	ig.mu.Lock()
	defer ig.mu.Unlock()
	if m, ok := ig.matchers[dir]; ok {
		return m
	}
	all := append([]gitignore.Pattern(nil), commonIgnorePatterns()...)
	cur := ig.root
	all = append(all, ig.patternsIn(cur)...)
	if rel, _ := filepath.Rel(ig.root, dir); rel != "" && rel != "." {
		for _, part := range pathComponents(rel) {
			cur = filepath.Join(cur, part)
			all = append(all, ig.patternsIn(cur)...)
		}
	}
	m := gitignore.NewMatcher(all)
	ig.matchers[dir] = m
	return m
}

// ignored reports whether path should be skipped. extra are base-name globs
// (ls's "ignore" parameter).
func (ig *ignorer) ignored(path string, isDir bool, extra []string) bool {
	base := filepath.Base(path)
	if isDir && fastIgnoreDirs[base] {
		return true
	}
	for _, pat := range extra {
		if ok, err := filepath.Match(pat, base); err == nil && ok {
			return true
		}
	}
	if path == ig.root {
		return false
	}
	rel, err := filepath.Rel(ig.root, path)
	if err != nil {
		rel = path
	}
	comps := pathComponents(rel)
	if len(comps) == 0 {
		return false
	}
	return ig.matcherFor(filepath.Dir(path)).Match(comps, isDir)
}
