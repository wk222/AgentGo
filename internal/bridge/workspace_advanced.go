package bridge

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type WorkspaceSearchMatch struct {
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	LineText  string `json:"lineText"`
	MatchText string `json:"matchText"`
	Length    int    `json:"length"`
}

type WorkspaceFileSearchResult struct {
	FilePath string                 `json:"filePath"`
	FileName string                 `json:"fileName"`
	Matches  []WorkspaceSearchMatch `json:"matches"`
}

type WorkspaceGitItem struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Staged bool   `json:"staged"`
}

// WorkspaceSearch performs full-text or regex search across workspace files.
func (s *AppService) WorkspaceSearch(query string, isRegex, matchCase, matchWholeWord bool, includePattern, excludePattern string, maxResults int) map[string]any {
	root := s.rt.WorkspaceRoot()
	if strings.TrimSpace(query) == "" {
		return map[string]any{"success": true, "results": []WorkspaceFileSearchResult{}, "totalMatches": 0, "totalFiles": 0}
	}

	if maxResults <= 0 || maxResults > 500 {
		maxResults = 200
	}

	var pattern *regexp.Regexp
	var err error

	patternStr := query
	if !isRegex {
		patternStr = regexp.QuoteMeta(query)
	}
	if matchWholeWord {
		patternStr = `\b` + patternStr + `\b`
	}
	if !matchCase {
		patternStr = `(?i)` + patternStr
	}

	pattern, err = regexp.Compile(patternStr)
	if err != nil {
		return map[string]any{"success": false, "error": fmt.Sprintf("invalid search pattern: %v", err)}
	}

	var results []WorkspaceFileSearchResult
	totalMatches := 0

	// Parse includes / excludes
	includes := splitPatterns(includePattern)
	excludes := splitPatterns(excludePattern)

	_ = filepath.WalkDir(root, func(filePath string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || totalMatches >= maxResults {
			return nil
		}

		rel, err := filepath.Rel(root, filePath)
		if err != nil || rel == "." {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		if d.IsDir() {
			name := d.Name()
			if shouldSkipWorkspaceEntry(root, filepath.ToSlash(filepath.Dir(rel)), name, true) {
				return filepath.SkipDir
			}
			return nil
		}

		name := d.Name()
		if shouldSkipWorkspaceEntry(root, filepath.ToSlash(filepath.Dir(rel)), name, false) {
			return nil
		}

		// Check includes / excludes
		if len(includes) > 0 && !matchesAnyPattern(includes, relSlash, name) {
			return nil
		}
		if len(excludes) > 0 && matchesAnyPattern(excludes, relSlash, name) {
			return nil
		}

		info, err := d.Info()
		if err != nil || info.Size() > 2*1024*1024 { // skip files larger than 2MB
			return nil
		}

		file, err := os.Open(filePath)
		if err != nil {
			return nil
		}
		defer file.Close()

		// Quick check if binary
		buf := make([]byte, 512)
		n, _ := file.Read(buf)
		if n > 0 && isBinaryBytes(buf[:n]) {
			return nil
		}
		_, _ = file.Seek(0, io.SeekStart)

		scanner := bufio.NewScanner(file)
		lineNum := 1
		var fileMatches []WorkspaceSearchMatch

		for scanner.Scan() {
			lineText := scanner.Text()
			locs := pattern.FindAllStringIndex(lineText, -1)
			for _, loc := range locs {
				matchStr := lineText[loc[0]:loc[1]]
				fileMatches = append(fileMatches, WorkspaceSearchMatch{
					Line:      lineNum,
					Column:    loc[0] + 1,
					LineText:  lineText,
					MatchText: matchStr,
					Length:    loc[1] - loc[0],
				})
				totalMatches++
				if totalMatches >= maxResults {
					break
				}
			}
			if totalMatches >= maxResults {
				break
			}
			lineNum++
		}

		if len(fileMatches) > 0 {
			results = append(results, WorkspaceFileSearchResult{
				FilePath: relSlash,
				FileName: name,
				Matches:  fileMatches,
			})
		}

		return nil
	})

	return map[string]any{
		"success":      true,
		"results":      results,
		"totalMatches": totalMatches,
		"totalFiles":   len(results),
	}
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
		part = strings.TrimSpace(part)
		if part != "" {
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

// WorkspaceGitStatus gets full status of Git working tree.
func (s *AppService) WorkspaceGitStatus() map[string]any {
	root := s.rt.WorkspaceRoot()
	out, err := runGit(root, "status", "--porcelain=v1", "-b")
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}
	}

	lines := strings.Split(out, "\n")
	branch := "main"
	var staged []WorkspaceGitItem
	var unstaged []WorkspaceGitItem
	var untracked []WorkspaceGitItem

	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if i == 0 && strings.HasPrefix(line, "## ") {
			b := strings.TrimPrefix(line, "## ")
			if idx := strings.Index(b, "..."); idx >= 0 {
				b = b[:idx]
			}
			branch = strings.TrimSpace(b)
			continue
		}
		if len(line) < 4 {
			continue
		}

		x := line[0] // index status
		y := line[1] // worktree status
		rel := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(rel, " -> "); idx >= 0 {
			rel = strings.TrimSpace(rel[idx+4:])
		}
		rel = strings.Trim(rel, "\"")
		name := filepath.Base(rel)

		if x == '?' && y == '?' {
			untracked = append(untracked, WorkspaceGitItem{
				Path:   rel,
				Name:   name,
				Status: "??",
				Staged: false,
			})
			continue
		}

		if x != ' ' && x != '?' {
			staged = append(staged, WorkspaceGitItem{
				Path:   rel,
				Name:   name,
				Status: string(x),
				Staged: true,
			})
		}
		if y != ' ' && y != '?' {
			unstaged = append(unstaged, WorkspaceGitItem{
				Path:   rel,
				Name:   name,
				Status: string(y),
				Staged: false,
			})
		}
	}

	return map[string]any{
		"success":   true,
		"branch":    branch,
		"staged":    staged,
		"unstaged":  unstaged,
		"untracked": untracked,
		"total":     len(staged) + len(unstaged) + len(untracked),
	}
}

// WorkspaceGitStage stages a file or all files.
func (s *AppService) WorkspaceGitStage(relPath string) map[string]any {
	root := s.rt.WorkspaceRoot()
	clean := strings.TrimSpace(relPath)
	var out string
	var err error
	if clean == "" || clean == "." {
		out, err = runGit(root, "add", "-A")
	} else {
		out, err = runGit(root, "add", "--", clean)
	}
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "output": out}
	}
	return map[string]any{"success": true, "output": out}
}

// WorkspaceGitUnstage unstages a file or all files.
func (s *AppService) WorkspaceGitUnstage(relPath string) map[string]any {
	root := s.rt.WorkspaceRoot()
	clean := strings.TrimSpace(relPath)
	var out string
	var err error
	if clean == "" || clean == "." {
		out, err = runGit(root, "restore", "--staged", ".")
		if err != nil {
			out, err = runGit(root, "reset", "HEAD", ".")
		}
	} else {
		out, err = runGit(root, "restore", "--staged", "--", clean)
		if err != nil {
			out, err = runGit(root, "reset", "HEAD", "--", clean)
		}
	}
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "output": out}
	}
	return map[string]any{"success": true, "output": out}
}

// WorkspaceGitCommit creates a commit.
func (s *AppService) WorkspaceGitCommit(message string) map[string]any {
	root := s.rt.WorkspaceRoot()
	msg := strings.TrimSpace(message)
	if msg == "" {
		return map[string]any{"success": false, "error": "commit message is empty"}
	}
	out, err := runGit(root, "commit", "-m", msg)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "output": out}
	}
	return map[string]any{"success": true, "output": out}
}

// WorkspaceGitBranches lists branches.
func (s *AppService) WorkspaceGitBranches() map[string]any {
	root := s.rt.WorkspaceRoot()
	out, err := runGit(root, "branch", "--list", "--no-color")
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	lines := strings.Split(out, "\n")
	var branches []string
	current := "main"
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "* ") {
			b := strings.TrimPrefix(l, "* ")
			current = b
			branches = append(branches, b)
		} else {
			branches = append(branches, l)
		}
	}
	return map[string]any{
		"success":  true,
		"current":  current,
		"branches": branches,
	}
}

// WorkspaceGitCheckout switches branch.
func (s *AppService) WorkspaceGitCheckout(branch string) map[string]any {
	root := s.rt.WorkspaceRoot()
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return map[string]any{"success": false, "error": "empty branch name"}
	}
	out, err := runGit(root, "checkout", branch)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "output": out}
	}
	return map[string]any{"success": true, "output": out}
}

// WorkspaceReadBase64 reads a binary or media file as base64 data.
func (s *AppService) WorkspaceReadBase64(relPath string) map[string]any {
	root := s.rt.WorkspaceRoot()
	clean, full, err := workspaceFullPath(root, relPath, false)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	info, err := os.Stat(full)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if info.IsDir() {
		return map[string]any{"success": false, "error": "path is a directory"}
	}
	if info.Size() > 20*1024*1024 {
		return map[string]any{"success": false, "error": "file too large for preview (20MB limit)"}
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}

	mimeType := http.DetectContentType(b)
	ext := strings.ToLower(filepath.Ext(clean))
	switch ext {
	case ".svg":
		mimeType = "image/svg+xml"
	case ".png":
		mimeType = "image/png"
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".ico":
		mimeType = "image/x-icon"
	case ".bmp":
		mimeType = "image/bmp"
	}

	b64 := base64.StdEncoding.EncodeToString(b)
	return map[string]any{
		"success":  true,
		"path":     clean,
		"mime":     mimeType,
		"size":     info.Size(),
		"base64":   b64,
		"data_url": "data:" + mimeType + ";base64," + b64,
	}
}

// WorkspaceDiagnostics retrieves active LSP diagnostics across the workspace.
func (s *AppService) WorkspaceDiagnostics() map[string]any {
	tb := s.getCodetools()
	if tb == nil {
		return map[string]any{"success": false, "total": 0, "servers": map[string]any{}}
	}
	states := tb.LSPStates()
	servers := make(map[string]any, len(states))
	total := 0
	for name, st := range states {
		total += st.DiagnosticCount
		diags := tb.LSPDiagnostics(name)
		servers[name] = map[string]any{
			"state":            st.State,
			"diagnostic_count": st.DiagnosticCount,
			"diagnostics":      diags,
		}
	}
	return map[string]any{
		"success": true,
		"total":   total,
		"servers": servers,
	}
}
