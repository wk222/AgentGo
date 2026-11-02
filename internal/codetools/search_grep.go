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
	OutputMode  string `json:"output_mode"`
	Offset      int    `json:"offset"`
	Context     int    `json:"context"`
}

// Output modes of grep.
const (
	modeContent = "content" // matching lines, grouped by file (the default)
	modeFiles   = "files"   // only the files that match
	modeCount   = "count"   // matches per file, and the total
)

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
		Description: fmt.Sprintf("Search the contents of files for a regular expression (or literal text). "+
			"By default it lists the matching lines, grouped by file with the most recently modified file first, %d matches per page; "+
			"when the answer says it was cut, call again with `offset` for the next page. "+
			"Use output_mode \"count\" to learn how many matches there are (per file and in total), and \"files\" to list only the files that match. "+
			"Follows .gitignore and skips hidden files and binaries. To search by file name instead, use glob.", s.Limit),
		Schema: objSchema(map[string]any{
			"pattern":      prop("string", "Regular expression to look for inside files"),
			"path":         prop("string", "Folder to search; defaults to the working directory"),
			"include":      prop("string", `Only search files whose name matches this glob, for example "*.go" or "*.{ts,tsx}"`),
			"literal_text": prop("boolean", "Treat pattern as plain text, with no special characters (default false)"),
			"output_mode": map[string]any{
				"type": "string", "enum": []string{modeContent, modeFiles, modeCount},
				"description": `"content" (default): matching lines. "files": only the paths of the files that match. "count": matches per file plus the total`,
			},
			"offset":  prop("integer", "How many results to skip before the first one shown (0 = start at the top); counts matches in content mode and files in files and count mode"),
			"context": prop("integer", fmt.Sprintf("Content mode only: lines to show before and after each match, 0 to %d (default 0)", maxContext)),
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
	mode := strings.ToLower(strings.TrimSpace(a.OutputMode))
	switch mode {
	case "":
		mode = modeContent
	case modeContent, modeFiles, modeCount:
	default:
		return toolErr(fmt.Sprintf("output_mode %q is not known; use %q, %q or %q.", a.OutputMode, modeContent, modeFiles, modeCount))
	}
	offset := max(a.Offset, 0)
	root := s.resolveRoot(a.Path)
	if msg, ok := s.allowRoot(ctx, sessionID, callID, "grep", "search", root); !ok {
		return toolErr(msg)
	}

	// Content mode needs enough candidates to reach the requested page; the
	// other two modes report totals, so they look as far as the ceiling allows.
	gather := maxPool
	if mode == modeContent {
		gather = poolFor(offset + s.Limit)
	}
	ctx, cancel := context.WithTimeout(ctx, s.GrepTimeout)
	defer cancel()
	res, err := s.searchFiles(ctx, pattern, root, a.Include, gather)
	switch {
	case errors.Is(err, errSearchTimeout):
		return toolErr(fmt.Sprintf("The search took longer than %s and found nothing yet. Narrow `path`, add `include`, or make the pattern more specific.", s.GrepTimeout))
	case err != nil:
		return toolErr("Search failed: " + err.Error())
	case len(res.matches) == 0:
		return toolOK(fmt.Sprintf("No matches for %q under %s.", a.Pattern, filepath.ToSlash(root)))
	}
	switch mode {
	case modeFiles:
		return toolOK(renderFiles(res, offset, s.Limit))
	case modeCount:
		return toolOK(renderCount(res, offset, s.Limit))
	}
	return toolOK(renderContent(res, offset, s.Limit, min(max(a.Context, 0), maxContext)))
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

// searchResult is what a search found: every candidate it gathered, ordered by
// file modification time (newest first), then path, then line.
type searchResult struct {
	matches []grepMatch
	// capped says more matches may exist: the search stopped at its candidate
	// limit or ran out of time, so len(matches) is only a lower bound.
	capped bool
}

// searchFiles runs the pattern with ripgrep, or with the Go walker when ripgrep
// is missing or refuses it, and gathers at most gather candidates.
func (s *searcher) searchFiles(ctx context.Context, pattern, root, include string, gather int) (searchResult, error) {
	matches, full, err := s.viaRipgrep(ctx, pattern, root, include, gather)
	if err != nil {
		if matches, full, err = s.viaWalk(ctx, pattern, root, include, gather); err != nil {
			return searchResult{}, err
		}
	}
	timedOut := ctx.Err() != nil
	if timedOut && len(matches) == 0 {
		return searchResult{}, errSearchTimeout
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
	return searchResult{matches: matches, capped: full || timedOut}, nil
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

// viaRipgrep gathers up to pool matches; full reports that it stopped there with
// more still to read.
func (s *searcher) viaRipgrep(ctx context.Context, pattern, root, include string, pool int) (matches []grepMatch, full bool, err error) {
	rg := s.Rg()
	if rg == "" {
		return nil, false, errNoRipgrep
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
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}

	mtimes := map[string]time.Time{}
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
		return matches, full, nil
	}
	if waitErr != nil && len(matches) == 0 {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) && ee.ExitCode() == 1 {
			return nil, false, nil // ripgrep's "no match"
		}
		return nil, false, waitErr
	}
	// Exit code 2 with matches means some paths were unreadable; what was found stands.
	return matches, false, nil
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

func (s *searcher) viaWalk(ctx context.Context, pattern, root, include string, pool int) (matches []grepMatch, full bool, err error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, false, fmt.Errorf("the pattern is not a valid regular expression: %w", err)
	}
	wanted, err := includeFilter(include)
	if err != nil {
		return nil, false, err
	}
	skip := newIgnorer(root)
	matches = []grepMatch{}

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
			full = true
			return walkStop
		}
		return walkContinue
	})
	return matches, full, nil
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
