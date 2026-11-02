package codetools

// grep: find lines matching a pattern, newest file first.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"agentgo/internal/coderuntime"
	"agentgo/internal/processutil"
)

const (
	maxWalkFileSize = 8 << 20 // the Go walker skips files above this; ripgrep copes on its own
	binarySniffSize = 8000    // bytes inspected for a NUL when deciding a file is binary
)

var (
	errNoRipgrep     = errors.New("ripgrep is not available")
	errSearchTimeout = errors.New("the search ran out of time before finding anything")
)

type grepArgs struct {
	Pattern     string `json:"pattern"`
	Path        string `json:"path"`
	Include     string `json:"include"`
	LiteralText bool   `json:"literal_text"`
}

// grepMatch is one matching line; lineNum and charNum are 1-based, and charNum
// counts UTF-16 units so a language server can be asked about the same spot.
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
		Description: fmt.Sprintf("Search the contents of files for a regular expression (or literal text) and list the matching lines, "+
			"grouped by file with the most recently modified file first, at most %d matches per call. "+
			"Follows .gitignore and skips hidden files and binaries. To search by file name instead, use glob.", grepLimit),
		Schema: objSchema(map[string]any{
			"pattern":      prop("string", "Regular expression to look for inside files"),
			"path":         prop("string", "Folder to search; defaults to the working directory"),
			"include":      prop("string", `Only search files whose name matches this glob, for example "*.go" or "*.{ts,tsx}"`),
			"literal_text": prop("boolean", "Treat pattern as plain text, with no special characters (default false)"),
		}, "pattern"),
		Parallel: true,
		Run: func(ctx context.Context, sessionID, callID, argsJSON string) (coderuntime.ToolResult, error) {
			var a grepArgs
			if r, bad := decode(argsJSON, &a); bad {
				return r, nil
			}
			return s.grep(ctx, sessionID, callID, a), nil
		},
	}
}

func (s *searcher) grep(ctx context.Context, sessionID, callID string, a grepArgs) coderuntime.ToolResult {
	if a.Pattern == "" {
		return toolErr("pattern is required.")
	}
	pattern := a.Pattern
	if a.LiteralText {
		pattern = regexp.QuoteMeta(pattern)
	}
	root := s.resolveRoot(a.Path)
	if msg, ok := s.allowRoot(ctx, sessionID, callID, "grep", "search", root); !ok {
		return toolErr(msg)
	}

	ctx, cancel := context.WithTimeout(ctx, s.GrepTimeout)
	defer cancel()
	matches, truncated, err := s.searchFiles(ctx, pattern, root, a.Include)
	switch {
	case errors.Is(err, errSearchTimeout):
		return toolErr(fmt.Sprintf("The search took longer than %s and found nothing yet. Narrow `path`, add `include`, or make the pattern more specific.", s.GrepTimeout))
	case err != nil:
		return toolErr("Search failed: " + err.Error())
	case len(matches) == 0:
		return toolOK(fmt.Sprintf("No matches for %q under %s.", a.Pattern, filepath.ToSlash(root)))
	}
	return toolOK(renderGrep(matches, truncated))
}

// renderGrep lists matches under one heading per file. The first line carries
// the count and, when the list was cut, says so: a bare "100 matches" reads as
// the total, and a model asked to count answered with the page size.
func renderGrep(matches []grepMatch, truncated bool) string {
	files := 0
	for i, m := range matches {
		if i == 0 || m.path != matches[i-1].path {
			files++
		}
	}
	var b strings.Builder
	if truncated {
		fmt.Fprintf(&b, "%d matches in %s, truncated: the search stopped at its %d-match limit and more exist, so this is not the total.\n",
			len(matches), plural(files, "file", "files"), grepLimit)
	} else {
		fmt.Fprintf(&b, "%s in %s, newest file first.\n", plural(len(matches), "match", "matches"), plural(files, "file", "files"))
	}
	for i, m := range matches {
		if i == 0 || m.path != matches[i-1].path {
			fmt.Fprintf(&b, "\n%s\n", filepath.ToSlash(m.path))
		}
		fmt.Fprintf(&b, "  %d:%d: %s\n", m.lineNum, m.charNum, clip(m.lineText, maxMatchWidth))
	}
	if truncated {
		b.WriteString("\nNarrow the search with a more specific path, include glob or pattern. To get a total count, run a counting command in the shell.\n")
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// clip shortens s to at most n characters, marking the cut.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// searchFiles runs the pattern with ripgrep, or with the Go walker when ripgrep
// is missing or refuses it, and returns at most grepLimit matches ordered by
// file modification time (newest first), then path, then line. truncated says
// more matches existed.
func (s *searcher) searchFiles(ctx context.Context, pattern, root, include string) (matches []grepMatch, truncated bool, err error) {
	matches, err = s.viaRipgrep(ctx, pattern, root, include)
	if err != nil {
		if matches, err = s.viaWalk(ctx, pattern, root, include); err != nil {
			return nil, false, err
		}
	}
	timedOut := ctx.Err() != nil
	if timedOut && len(matches) == 0 {
		return nil, false, errSearchTimeout
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		switch {
		case !a.modTime.Equal(b.modTime):
			return a.modTime.After(b.modTime)
		case a.path != b.path:
			return a.path < b.path
		}
		return a.lineNum < b.lineNum
	})
	truncated = timedOut || len(matches) > grepLimit
	if len(matches) > grepLimit {
		matches = matches[:grepLimit]
	}
	return matches, truncated, nil
}

// --- ripgrep backend ----------------------------------------------------------

// rgEvent is the part of a `rg --json` record that is read.
type rgEvent struct {
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

func (s *searcher) viaRipgrep(ctx context.Context, pattern, root, include string) ([]grepMatch, error) {
	rg := s.Rg()
	if rg == "" {
		return nil, errNoRipgrep
	}
	// --no-require-git: honour .gitignore files even when the folder is not (yet) a git repository.
	args := []string{"--json", "--line-number", "--with-filename", "--no-require-git", "-e", pattern}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, rgIgnoreFlags(root)...)
	args = append(args, "--", root)

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(runCtx, rg, args...)
	processutil.HideWindow(cmd) // the desktop build has no console; a child would flash its own window
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var (
		matches []grepMatch
		mtimes  = map[string]time.Time{}
		pool    = grepLimit * poolPerResult
		full    = false
	)
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var ev rgEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != "match" || len(ev.Data.Submatches) == 0 || ev.Data.Path.Text == "" {
			continue
		}
		p := ev.Data.Path.Text
		if inNoiseDir(root, p) || isNoiseFileName(filepath.Base(p)) {
			continue
		}
		mod, seen := mtimes[p]
		if !seen {
			fi, err := os.Stat(p)
			if err != nil {
				continue
			}
			mod = fi.ModTime()
			mtimes[p] = mod
		}
		line := ev.Data.Lines.Text
		matches = append(matches, grepMatch{
			path: p, modTime: mod, lineNum: ev.Data.LineNumber,
			charNum:  utf16Column(line, ev.Data.Submatches[0].Start),
			lineText: strings.TrimSpace(line),
		})
		if len(matches) >= pool {
			full = true
			stop() // enough candidates; do not let a huge tree run to the end
			break
		}
	}
	waitErr := cmd.Wait()
	if full || ctx.Err() != nil {
		return matches, nil
	}
	if waitErr != nil && len(matches) == 0 {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) && ee.ExitCode() == 1 {
			return nil, nil // ripgrep's "no match"
		}
		return nil, waitErr
	}
	// Exit code 2 with matches means some paths were unreadable; what was found stands.
	return matches, nil
}

// utf16Column converts a byte offset within line to a 1-based UTF-16 column.
func utf16Column(line string, byteOffset int) int {
	if byteOffset > len(line) {
		byteOffset = len(line)
	}
	n := 0
	for _, r := range line[:byteOffset] {
		n += len(utf16.Encode([]rune{r}))
	}
	return n + 1
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// rgIgnoreFlags points ripgrep at the project ignore files it would not find on
// its own. It reads .gitignore itself.
func rgIgnoreFlags(root string) []string {
	var flags []string
	for _, name := range projectIgnoreFiles {
		if p := filepath.Join(root, name); name != ".gitignore" && fileExists(p) {
			flags = append(flags, "--ignore-file", p)
		}
	}
	return flags
}

// --- Go walker backend --------------------------------------------------------

func (s *searcher) viaWalk(ctx context.Context, pattern, root, include string) ([]grepMatch, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("the pattern is not a valid regular expression: %w", err)
	}
	wanted, err := includeFilter(include)
	if err != nil {
		return nil, err
	}
	skip := newIgnorer(root)
	pool := grepLimit * poolPerResult
	matches := []grepMatch{}

	walkTree(ctx, root, walkOpts{}, func(p string, isDir bool) walkAction {
		if p == root {
			return walkContinue
		}
		hidden := strings.HasPrefix(filepath.Base(p), ".")
		if isDir {
			if hidden || skip.ignored(p, true, nil) {
				return walkSkipDir
			}
			return walkContinue
		}
		if hidden || skip.ignored(p, false, nil) {
			return walkContinue
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			rel = p
		}
		if !wanted(filepath.ToSlash(rel)) {
			return walkContinue
		}
		hits := scanFile(p, re)
		if len(hits) == 0 {
			return walkContinue
		}
		fi, err := os.Stat(p)
		if err != nil {
			return walkContinue
		}
		for _, h := range hits {
			h.path, h.modTime = p, fi.ModTime()
			matches = append(matches, h)
		}
		if len(matches) >= pool {
			return walkStop
		}
		return walkContinue
	})
	return matches, nil
}

// includeFilter compiles the include glob. Like ripgrep's --glob, a pattern with
// no slash is compared with the file name at any depth; one with a slash is
// compared with the path below the search root.
func includeFilter(glob string) (func(rel string) bool, error) {
	if glob == "" {
		return func(string) bool { return true }, nil
	}
	if !doublestar.ValidatePattern(glob) {
		return nil, fmt.Errorf("the include glob %q is not valid", glob)
	}
	if !strings.Contains(glob, "/") {
		return func(rel string) bool {
			ok, _ := doublestar.Match(glob, rel[strings.LastIndex(rel, "/")+1:])
			return ok
		}, nil
	}
	anchored := strings.TrimPrefix(glob, "/")
	return func(rel string) bool { ok, _ := doublestar.Match(anchored, rel); return ok }, nil
}

// scanFile returns the matching lines of a text file; a binary or oversized
// file yields none.
func scanFile(path string, re *regexp.Regexp) []grepMatch {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || st.Size() > maxWalkFileSize {
		return nil
	}
	head := make([]byte, binarySniffSize)
	n, _ := io.ReadFull(f, head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return nil
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil
	}

	var out []grepMatch
	rd := bufio.NewReader(f)
	for num := 1; ; num++ {
		line, err := rd.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if loc := re.FindStringIndex(line); loc != nil {
				out = append(out, grepMatch{lineNum: num, charNum: utf16Column(line, loc[0]), lineText: strings.TrimSpace(line)})
			}
		}
		if err != nil {
			return out
		}
	}
}
