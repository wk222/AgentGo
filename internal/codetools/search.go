package codetools

// Native grep / glob / ls. Output formats, limits and the ripgrep-first,
// pure-Go-fallback strategy are derived from Crush's tools (licenses/crush/NOTICE.md,
// FSL-1.1-MIT); the walking and ignore logic is our own (search_walk.go,
// search_ignore.go), with no Crush package behind it.

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/charmbracelet/x/ansi"

	"agentgo/internal/coderuntime"
)

const (
	grepLimit           = 100
	globLimit           = 100
	maxGrepContentWidth = 500
	maxLSFiles          = 1000
)

// SearchOptions configures NativeSearch.
type SearchOptions struct {
	WorkDir string
	// Approve gates listing directories outside WorkDir. nil allows all.
	Approve func(ctx context.Context, req coderuntime.FSRequest) bool
	// Rg returns the ripgrep binary, or "" to use the pure-Go walker. nil means
	// look "rg" up on PATH.
	Rg          func() string
	GrepTimeout time.Duration // default 5s
	GlobTimeout time.Duration // default 30s
}

type searcher struct{ SearchOptions }

// normalizeSearch fills in the defaults: rg from PATH, 5s grep and 30s glob.
func normalizeSearch(opts SearchOptions) SearchOptions {
	if opts.Rg == nil {
		opts.Rg = func() string { p, _ := exec.LookPath("rg"); return p }
	}
	opts.GrepTimeout = cmp.Or(opts.GrepTimeout, 5*time.Second)
	opts.GlobTimeout = cmp.Or(opts.GlobTimeout, 30*time.Second)
	return opts
}

// NativeSearch returns the grep, glob and ls tools.
func NativeSearch(opts SearchOptions) []coderuntime.Tool {
	s := &searcher{normalizeSearch(opts)}
	return []coderuntime.Tool{s.grepTool(), s.globTool(), s.lsTool()}
}

func toolErr(s string) coderuntime.ToolResult {
	return coderuntime.ToolResult{Content: s, IsError: true}
}
func toolOK(s string) coderuntime.ToolResult { return coderuntime.ToolResult{Content: s} }

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, desc string) map[string]any { return map[string]any{"type": typ, "description": desc} }

func decode(argsJSON string, into any) (coderuntime.ToolResult, bool) {
	if strings.TrimSpace(argsJSON) == "" {
		argsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(argsJSON), into); err != nil {
		return toolErr("invalid arguments: " + err.Error()), true
	}
	return coderuntime.ToolResult{}, false
}

// --- grep ----------------------------------------------------------------------

type grepArgs struct {
	Pattern     string `json:"pattern"`
	Path        string `json:"path"`
	Include     string `json:"include"`
	LiteralText bool   `json:"literal_text"`
}

type grepMatch struct {
	path     string
	modTime  time.Time
	lineNum  int
	charNum  int
	lineText string
}

func (s *searcher) grepTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "grep",
		Description: fmt.Sprintf("Search file contents by regex or literal text; returns matching lines grouped by file, files sorted by modification time (max %d matches); "+
			"respects .gitignore. Use glob to filter by filename, not contents.", grepLimit),
		Schema: objSchema(map[string]any{
			"pattern":      prop("string", "The regex pattern to search for in file contents"),
			"path":         prop("string", "The directory to search in. Defaults to the current working directory."),
			"include":      prop("string", `File pattern to include in the search (e.g. "*.js", "*.{ts,tsx}")`),
			"literal_text": prop("boolean", "If true, the pattern will be treated as literal text with special regex characters escaped. Default is false."),
		}, "pattern"),
		Parallel: true,
		Run: func(ctx context.Context, _, _, argsJSON string) (coderuntime.ToolResult, error) {
			var a grepArgs
			if r, bad := decode(argsJSON, &a); bad {
				return r, nil
			}
			return s.grep(ctx, a), nil
		},
	}
}

func (s *searcher) grep(ctx context.Context, a grepArgs) coderuntime.ToolResult {
	if a.Pattern == "" {
		return toolErr("pattern is required")
	}
	pattern := a.Pattern
	if a.LiteralText {
		pattern = regexp.QuoteMeta(pattern)
	}
	root := cmp.Or(a.Path, s.WorkDir)

	ctx, cancel := context.WithTimeout(ctx, s.GrepTimeout)
	defer cancel()

	matches, truncated, err := s.searchFiles(ctx, pattern, root, a.Include)
	if err != nil {
		return toolErr(fmt.Sprintf("error searching files: %v", err))
	}
	if len(matches) == 0 {
		return toolOK("No files found")
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Found %d matches\n", len(matches))
	current := ""
	for _, m := range matches {
		if current != m.path {
			if current != "" {
				out.WriteString("\n")
			}
			current = m.path
			fmt.Fprintf(&out, "%s:\n", filepath.ToSlash(m.path))
		}
		if m.lineNum <= 0 {
			fmt.Fprintf(&out, "  %s\n", m.path)
			continue
		}
		text := m.lineText
		if ansi.StringWidth(text) > maxGrepContentWidth {
			text = ansi.Truncate(text, maxGrepContentWidth, "...")
		}
		if m.charNum > 0 {
			fmt.Fprintf(&out, "  Line %d, Char %d: %s\n", m.lineNum, m.charNum, text)
		} else {
			fmt.Fprintf(&out, "  Line %d: %s\n", m.lineNum, text)
		}
	}
	if truncated {
		out.WriteString("\n(Results are truncated. Consider using a more specific path or pattern.)")
	}
	return toolOK(out.String())
}

func (s *searcher) searchFiles(ctx context.Context, pattern, root, include string) ([]grepMatch, bool, error) {
	matches, err := s.grepWithRg(ctx, pattern, root, include)
	if err != nil {
		if matches, err = s.grepWalk(ctx, pattern, root, include); err != nil {
			return nil, false, err
		}
	}
	// Stable: several matches in one file share a modTime and keep line order.
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].modTime.After(matches[j].modTime) })
	truncated := len(matches) > grepLimit
	if truncated {
		matches = matches[:grepLimit]
	}
	return matches, truncated, nil
}

type rgMatch struct {
	Type string `json:"type"`
	Data struct {
		Path       struct{ Text string } `json:"path"`
		Lines      struct{ Text string } `json:"lines"`
		LineNumber int                   `json:"line_number"`
		Submatches []struct {
			Start int `json:"start"`
		} `json:"submatches"`
	} `json:"data"`
}

func (s *searcher) grepWithRg(ctx context.Context, pattern, root, include string) ([]grepMatch, error) {
	rg := s.Rg()
	if rg == "" {
		return nil, errors.New("ripgrep not available")
	}
	args := []string{"--json", "-H", "-n", "-0", pattern}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, root)
	cmd := exec.CommandContext(ctx, rg, args...)
	for _, name := range ignoreFileNames {
		if p := filepath.Join(root, name); fileExists(p) {
			cmd.Args = append(cmd.Args, "--ignore-file", p)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return []grepMatch{}, nil
		}
		return nil, err
	}
	var matches []grepMatch
	for line := range bytes.SplitSeq(bytes.TrimSpace(out), []byte{'\n'}) {
		var m rgMatch
		if len(line) == 0 || json.Unmarshal(line, &m) != nil || m.Type != "match" || len(m.Data.Submatches) == 0 {
			continue
		}
		fi, err := os.Stat(m.Data.Path.Text)
		if err != nil {
			continue
		}
		matches = append(matches, grepMatch{
			path: m.Data.Path.Text, modTime: fi.ModTime(), lineNum: m.Data.LineNumber,
			charNum: m.Data.Submatches[0].Start + 1, lineText: strings.TrimSpace(m.Data.Lines.Text),
		})
	}
	return matches, nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// grepWalk is the pure-Go search used when ripgrep is missing.
func (s *searcher) grepWalk(ctx context.Context, pattern, root, include string) ([]grepMatch, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}
	var incl *regexp.Regexp
	if include != "" {
		if incl, err = regexp.Compile(globToRegex(include)); err != nil {
			return nil, fmt.Errorf("invalid include pattern: %w", err)
		}
	}
	ig := newIgnorer(root)
	matches := []grepMatch{}
	walkTree(ctx, root, walkOpts{}, func(path string, isDir bool) walkAction {
		if isDir {
			if path != root && ig.ignored(path, true, nil) {
				return walkSkipDir
			}
			return walkContinue
		}
		if ig.ignored(path, false, nil) {
			return walkContinue
		}
		if base := filepath.Base(path); strings.HasPrefix(base, ".") {
			return walkContinue // hidden, like ripgrep
		}
		if incl != nil && !incl.MatchString(path) {
			return walkContinue
		}
		lines, err := matchingLines(path, re)
		if err != nil || len(lines) == 0 {
			return walkContinue
		}
		fi, err := os.Stat(path)
		if err != nil {
			return walkContinue
		}
		for _, lm := range lines {
			lm.path, lm.modTime = path, fi.ModTime()
			matches = append(matches, lm)
			if len(matches) >= 2*grepLimit {
				return walkStop
			}
		}
		return walkContinue
	})
	return matches, nil
}

// matchingLines returns every matching line (first match gives the column).
func matchingLines(path string, re *regexp.Regexp) ([]grepMatch, error) {
	if !isTextFile(path) {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []grepMatch
	r := bufio.NewReader(f)
	for n := 1; ; n++ {
		line, err := r.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if loc := re.FindStringIndex(line); loc != nil {
			out = append(out, grepMatch{lineNum: n, charNum: loc[0] + 1, lineText: line})
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func isTextFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false
	}
	ct := http.DetectContentType(buf[:n])
	return strings.HasPrefix(ct, "text/") || ct == "application/json" || ct == "application/xml" ||
		ct == "application/javascript" || ct == "application/x-sh"
}

var globBrace = regexp.MustCompile(`\{([^}]+)\}`)

// globToRegex turns an include glob ("*.{ts,tsx}") into an unanchored regex.
func globToRegex(glob string) string {
	r := strings.ReplaceAll(glob, ".", `\.`)
	r = strings.ReplaceAll(r, "*", ".*")
	r = strings.ReplaceAll(r, "?", ".")
	return globBrace.ReplaceAllStringFunc(r, func(m string) string {
		return "(" + strings.ReplaceAll(m[1:len(m)-1], ",", "|") + ")"
	})
}

// --- glob ----------------------------------------------------------------------

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func (s *searcher) globTool() coderuntime.Tool {
	return coderuntime.Tool{
		Name: "glob",
		Description: fmt.Sprintf("Find files by name/pattern (glob syntax), sorted by modification time; max %d results; skips hidden files. "+
			"Use grep to search file contents.", globLimit),
		Schema: objSchema(map[string]any{
			"pattern": prop("string", "The glob pattern to match files against"),
			"path":    prop("string", "The directory to search in. Defaults to the current working directory."),
		}, "pattern"),
		Parallel: true,
		Run: func(ctx context.Context, _, _, argsJSON string) (coderuntime.ToolResult, error) {
			var a globArgs
			if r, bad := decode(argsJSON, &a); bad {
				return r, nil
			}
			return s.glob(ctx, a), nil
		},
	}
}

func (s *searcher) glob(ctx context.Context, a globArgs) coderuntime.ToolResult {
	if a.Pattern == "" {
		return toolErr("pattern is required")
	}
	root := cmp.Or(a.Path, s.WorkDir)
	ctx, cancel := context.WithTimeout(ctx, s.GlobTimeout)
	defer cancel()

	files, truncated, err := s.globFiles(ctx, a.Pattern, root, globLimit)
	if err != nil {
		return toolErr(fmt.Sprintf("error finding files: %v", err))
	}
	if len(files) == 0 {
		return toolOK("No files found")
	}
	for i, p := range files {
		files[i] = filepath.ToSlash(p)
	}
	out := strings.Join(files, "\n")
	if truncated {
		out += "\n\n(Results are truncated. Consider using a more specific path or pattern.)"
	}
	return toolOK(out)
}

// splitGlobPrefix splits "internal/agent/*.go" into ("internal/agent", "*.go")
// so the walk starts at the literal directory.
func splitGlobPrefix(pattern string) (prefix, rest string) {
	pattern = filepath.ToSlash(pattern)
	segs := strings.Split(pattern, "/")
	var literal []string
	for i, seg := range segs {
		if strings.ContainsAny(seg, `*?[{\`) {
			return strings.Join(literal, "/"), strings.Join(segs[i:], "/")
		}
		literal = append(literal, seg)
	}
	if len(literal) == 0 {
		return "", pattern
	}
	return strings.Join(literal[:len(literal)-1], "/"), literal[len(literal)-1]
}

func (s *searcher) globFiles(ctx context.Context, pattern, root string, limit int) ([]string, bool, error) {
	prefix, rest := splitGlobPrefix(pattern)
	walkRoot, walkPattern := root, pattern
	if prefix != "" {
		walkRoot, walkPattern = filepath.Join(root, prefix), rest
	}
	if rg := s.Rg(); rg != "" {
		if matches, err := rgFiles(ctx, rg, walkPattern, walkRoot, limit); err == nil {
			return matches, len(matches) >= limit, nil
		}
	}
	return globWalk(ctx, walkPattern, walkRoot, limit)
}

// globWalk is the pure-Go glob, newest files first.
func globWalk(ctx context.Context, pattern, root string, limit int) ([]string, bool, error) {
	pattern = filepath.ToSlash(pattern)
	ig := newIgnorer(root)
	type hit struct {
		path string
		mod  time.Time
	}
	var hits []hit
	stopped := false
	walkTree(ctx, root, walkOpts{}, func(path string, isDir bool) walkAction {
		if path == root {
			return walkContinue
		}
		if ig.ignored(path, isDir, nil) {
			if isDir {
				return walkSkipDir
			}
			return walkContinue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		if ok, err := doublestar.Match(pattern, filepath.ToSlash(rel)); err != nil || !ok {
			return walkContinue
		}
		info, err := os.Stat(path)
		if err != nil {
			return walkContinue
		}
		hits = append(hits, hit{path, info.ModTime()})
		if limit > 0 && len(hits) >= limit*2 {
			stopped = true
			return walkStop
		}
		return walkContinue
	})
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })
	truncated := stopped
	if limit > 0 && len(hits) > limit {
		hits, truncated = hits[:limit], true
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.path
	}
	return out, truncated, nil
}

// rgFiles lists files with `rg --files`, streaming and capping the pool so a
// huge root cannot exhaust memory; shorter paths win.
func rgFiles(ctx context.Context, rg, pattern, root string, limit int) ([]string, error) {
	args := []string{"--files", "--null"}
	if pattern != "" {
		if !filepath.IsAbs(pattern) && !strings.HasPrefix(pattern, "/") {
			pattern = "/" + pattern
		}
		args = append(args, "--glob", pattern)
	}
	cmd := exec.CommandContext(ctx, rg, args...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pool := max(limit*20, 1000)
	var matches []string
	r := bufio.NewReader(stdout)
	for {
		p, err := r.ReadString(0)
		if p = strings.TrimRight(p, "\x00"); p != "" {
			abs := p
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(root, p)
			}
			if !skipHidden(abs) {
				matches = append(matches, abs)
			}
		}
		if err != nil || len(matches) >= pool {
			break
		}
	}
	_ = stdout.Close()
	if werr := cmd.Wait(); werr != nil && len(matches) == 0 {
		var ee *exec.ExitError
		if errors.As(werr, &ee) && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("ripgrep: %w\n%s", werr, stderr.String())
	}
	sort.SliceStable(matches, func(i, j int) bool { return len(matches[i]) < len(matches[j]) })
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

var noiseDirs = map[string]bool{
	".crush": true, "node_modules": true, "vendor": true, "dist": true, "build": true, "target": true,
	".git": true, ".idea": true, ".vscode": true, "__pycache__": true, "bin": true, "obj": true, "out": true,
	"coverage": true, "logs": true, "generated": true, "bower_components": true, "jspm_packages": true,
}

// skipHidden drops dotfiles and well-known noise directories from rg results.
func skipHidden(path string) bool {
	if base := filepath.Base(path); base != "." && strings.HasPrefix(base, ".") {
		return true
	}
	for part := range strings.SplitSeq(path, string(os.PathSeparator)) {
		if noiseDirs[part] {
			return true
		}
	}
	return false
}
