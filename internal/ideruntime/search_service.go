package ideruntime

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed tools/rg.exe
var embeddedRg []byte

// SearchQuery parameters for ripgrep search.
type SearchQuery struct {
	Pattern       string   `json:"pattern"`
	IsRegex       bool     `json:"is_regex"`
	CaseSensitive bool     `json:"case_sensitive"`
	WholeWord     bool     `json:"whole_word"`
	IncludeGlobs  []string `json:"include_globs,omitempty"`
	ExcludeGlobs  []string `json:"exclude_globs,omitempty"`
	MaxResults    int      `json:"max_results,omitempty"`
}

// SearchMatch represents a single matched line and range.
type SearchMatch struct {
	LineNumber int    `json:"line_number"`
	LineText   string `json:"line_text"`
	StartCol   int    `json:"start_col"`
	EndCol     int    `json:"end_col"`
}

// SearchResult groups matches by file.
type SearchResult struct {
	FileURI   string        `json:"file_uri"`
	FilePath  string        `json:"file_path"`
	Matches   []SearchMatch `json:"matches"`
}

// SearchService executes ripgrep commands through ProcessSupervisor.
type SearchService struct {
	supervisor *ProcessSupervisor
	fs         *WorkspaceFS
	rgPath     string
	initOnce   sync.Once
	initErr    error
}

// NewSearchService creates a search service linked to the workspace and supervisor.
func NewSearchService(supervisor *ProcessSupervisor, fs *WorkspaceFS) *SearchService {
	return &SearchService{
		supervisor: supervisor,
		fs:         fs,
	}
}

func (s *SearchService) resolveRgBinary() (string, error) {
	s.initOnce.Do(func() {
		// 1. If embedded ripgrep is available on Windows, extract to local appdata cache
		if len(embeddedRg) > 0 && runtime.GOOS == "windows" {
			cacheDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "AgentGo", "bin")
			_ = os.MkdirAll(cacheDir, 0755)
			target := filepath.Join(cacheDir, "rg.exe")

			// Write if missing or size differs
			needExtract := true
			if fi, err := os.Stat(target); err == nil && fi.Size() == int64(len(embeddedRg)) {
				needExtract = false
			}

			if needExtract {
				if err := os.WriteFile(target, embeddedRg, 0755); err == nil {
					s.rgPath = target
					return
				}
			} else {
				s.rgPath = target
				return
			}
		}

		// 2. Check if on system PATH
		if p, err := exec.LookPath("rg"); err == nil {
			s.rgPath = p
			return
		}
		if p, err := exec.LookPath("rg.exe"); err == nil {
			s.rgPath = p
			return
		}

		s.initErr = errors.New("ripgrep binary (rg) not found on PATH or embedded")
	})

	return s.rgPath, s.initErr
}

// SearchFiles returns matching filenames within workspace (like Ctrl+P).
func (s *SearchService) SearchFiles(ctx context.Context, pattern string, maxResults int) ([]string, error) {
	rg, err := s.resolveRgBinary()
	if err != nil {
		return nil, err
	}

	root := s.fs.RootPath()
	args := []string{"--files"}

	if pattern != "" {
		args = append(args, "--glob", "*"+pattern+"*")
	}
	args = append(args, ".")

	spec := ProcessSpec{
		ID:      fmt.Sprintf("search-files-%d", time.Now().UnixNano()),
		Kind:    KindSearch,
		Command: rg,
		Args:    args,
		Cwd:     root,
	}

	var buf bytes.Buffer
	var mu sync.Mutex

	h, err := s.supervisor.Spawn(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("spawn rg --files: %w", err)
	}

	h.OnOutput(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		buf.Write(data)
	})

	_ = h.Wait()

	mu.Lock()
	raw := buf.String()
	mu.Unlock()

	scanner := bufio.NewScanner(strings.NewReader(raw))
	results := make([]string, 0, 50)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		full := filepath.Join(root, line)
		results = append(results, PathToURI(full))
		if maxResults > 0 && len(results) >= maxResults {
			break
		}
	}
	return results, nil
}

// SearchText executes text search with ripgrep --json, returning grouped results.
func (s *SearchService) SearchText(ctx context.Context, query SearchQuery) ([]SearchResult, error) {
	rg, err := s.resolveRgBinary()
	if err != nil {
		return nil, err
	}

	if query.Pattern == "" {
		return nil, nil
	}

	root := s.fs.RootPath()
	args := []string{"--json"}

	if !query.IsRegex {
		args = append(args, "--fixed-strings")
	}
	if query.CaseSensitive {
		args = append(args, "--case-sensitive")
	} else {
		args = append(args, "--ignore-case")
	}
	if query.WholeWord {
		args = append(args, "--word-regexp")
	}
	for _, g := range query.IncludeGlobs {
		args = append(args, "--glob", g)
	}
	for _, g := range query.ExcludeGlobs {
		args = append(args, "--glob", "!"+g)
	}

	args = append(args, "--", query.Pattern, ".")

	spec := ProcessSpec{
		ID:      fmt.Sprintf("search-text-%d", time.Now().UnixNano()),
		Kind:    KindSearch,
		Command: rg,
		Args:    args,
		Cwd:     root,
	}

	var mu sync.Mutex
	var streamBuf bytes.Buffer

	h, err := s.supervisor.Spawn(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("spawn rg --json: %w", err)
	}

	h.OnOutput(func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		streamBuf.Write(data)
	})

	_ = h.Wait()

	mu.Lock()
	allData := streamBuf.Bytes()
	mu.Unlock()

	// Parse JSON lines from ripgrep
	scanner := bufio.NewScanner(bytes.NewReader(allData))
	resultsMap := make(map[string]*SearchResult)
	resultsOrder := make([]string, 0)
	totalMatches := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		if event.Type == "match" {
			var matchData struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				Lines struct {
					Text string `json:"text"`
				} `json:"lines"`
				LineNumber int `json:"line_number"`
				Submatches []struct {
					Start int `json:"start"`
					End   int `json:"end"`
				} `json:"submatches"`
			}
			if err := json.Unmarshal(event.Data, &matchData); err != nil {
				continue
			}

			relPath := matchData.Path.Text
			fullPath := filepath.Join(root, relPath)
			fileURI := PathToURI(fullPath)

			res, exists := resultsMap[fileURI]
			if !exists {
				res = &SearchResult{
					FileURI:  fileURI,
					FilePath: relPath,
					Matches:  make([]SearchMatch, 0),
				}
				resultsMap[fileURI] = res
				resultsOrder = append(resultsOrder, fileURI)
			}

			for _, sm := range matchData.Submatches {
				res.Matches = append(res.Matches, SearchMatch{
					LineNumber: matchData.LineNumber,
					LineText:   strings.TrimRight(matchData.Lines.Text, "\r\n"),
					StartCol:   sm.Start,
					EndCol:     sm.End,
				})
				totalMatches++
			}

			if query.MaxResults > 0 && totalMatches >= query.MaxResults {
				break
			}
		}
	}

	out := make([]SearchResult, 0, len(resultsOrder))
	for _, u := range resultsOrder {
		out = append(out, *resultsMap[u])
	}
	return out, nil
}

// Unused helper for hash check
var _ = hex.EncodeToString
