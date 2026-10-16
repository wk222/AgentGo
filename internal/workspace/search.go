package workspace

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// SearchMatch is one hit inside a file (JSON shape is consumed by the UI).
type SearchMatch struct {
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	LineText  string `json:"lineText"`
	MatchText string `json:"matchText"`
	Length    int    `json:"length"`
}

// FileSearchResult groups the hits of one file.
type FileSearchResult struct {
	FilePath string        `json:"filePath"`
	FileName string        `json:"fileName"`
	Matches  []SearchMatch `json:"matches"`
}

// SearchOptions mirrors the IDE search panel.
type SearchOptions struct {
	Query      string
	IsRegex    bool
	MatchCase  bool
	WholeWord  bool
	Include    string // comma separated globs
	Exclude    string // comma separated globs
	MaxResults int
}

const (
	searchMaxFileBytes = 2 * 1024 * 1024
	searchDefaultMax   = 200
	searchHardMax      = 500
)

// Search runs a full-text or regex search across the workspace. An empty query
// yields no results and no error.
func Search(root string, o SearchOptions) (results []FileSearchResult, total int, err error) {
	if strings.TrimSpace(o.Query) == "" {
		return []FileSearchResult{}, 0, nil
	}
	max := o.MaxResults
	if max <= 0 || max > searchHardMax {
		max = searchDefaultMax
	}

	expr := o.Query
	if !o.IsRegex {
		expr = regexp.QuoteMeta(o.Query)
	}
	if o.WholeWord {
		expr = `\b` + expr + `\b`
	}
	if !o.MatchCase {
		expr = `(?i)` + expr
	}
	pattern, err := regexp.Compile(expr)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid search pattern: %v", err)
	}

	includes := splitPatterns(o.Include)
	excludes := splitPatterns(o.Exclude)

	_ = filepath.WalkDir(root, func(filePath string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || total >= max {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil || rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		name := d.Name()
		dirRel := filepath.ToSlash(filepath.Dir(rel))

		if d.IsDir() {
			if ShouldSkipEntry(root, dirRel, name, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if ShouldSkipEntry(root, dirRel, name, false) {
			return nil
		}
		if len(includes) > 0 && !matchesAnyPattern(includes, relSlash, name) {
			return nil
		}
		if len(excludes) > 0 && matchesAnyPattern(excludes, relSlash, name) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > searchMaxFileBytes {
			return nil
		}

		hits, n := searchFile(filePath, pattern, max-total)
		if len(hits) > 0 {
			results = append(results, FileSearchResult{FilePath: relSlash, FileName: name, Matches: hits})
			total += n
		}
		return nil
	})
	return results, total, nil
}

// searchFile scans one file and returns at most limit matches.
func searchFile(filePath string, pattern *regexp.Regexp, limit int) ([]SearchMatch, int) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, 0
	}
	defer file.Close()

	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	if n > 0 && isBinaryBytes(buf[:n]) {
		return nil, 0
	}
	_, _ = file.Seek(0, io.SeekStart)

	var hits []SearchMatch
	scanner := bufio.NewScanner(file)
	for lineNum := 1; scanner.Scan(); lineNum++ {
		text := scanner.Text()
		for _, loc := range pattern.FindAllStringIndex(text, -1) {
			hits = append(hits, SearchMatch{
				Line: lineNum, Column: loc[0] + 1, LineText: text,
				MatchText: text[loc[0]:loc[1]], Length: loc[1] - loc[0],
			})
			if len(hits) >= limit {
				return hits, len(hits)
			}
		}
	}
	return hits, len(hits)
}

func isBinaryBytes(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

func splitPatterns(pat string) []string {
	var out []string
	for _, part := range strings.Split(pat, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func matchesAnyPattern(patterns []string, rel, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, rel); ok {
			return true
		}
		if ok, _ := path.Match(p, name); ok {
			return true
		}
		if ok, _ := path.Match("*"+p+"*", rel); ok {
			return true
		}
	}
	return false
}
