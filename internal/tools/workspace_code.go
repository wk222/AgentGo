package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/tool/utils"
)

const (
	maxAgentSourceBytes = 2 * 1024 * 1024
	maxSearchResults    = 200
	maxGitOutputBytes   = 64 * 1024
)

var agentIgnoredDirs = map[string]bool{
	".git": true, ".next": true, ".nuxt": true, ".venv": true,
	"__pycache__": true, "build": true, "coverage": true, "dist": true,
	"node_modules": true, "target": true, "vendor": true, "wandb": true,
	"checkpoints": true, "runs": true,
}

type readWorkspaceFileInput struct {
	Path      string `json:"path" jsonschema:"required,description=Workspace-relative file path"`
	StartLine int    `json:"start_line,omitempty" jsonschema:"description=First 1-based line to return; default 1"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"description=Last 1-based line to return; default start_line+399"`
}

type readWorkspaceFileOutput struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	StartLine  int    `json:"start_line"`
	EndLine    int    `json:"end_line"`
	TotalLines int    `json:"total_lines"`
	Truncated  bool   `json:"truncated"`
}

type searchWorkspaceInput struct {
	Query         string `json:"query" jsonschema:"required,description=Literal text or regular expression to search for"`
	Path          string `json:"path,omitempty" jsonschema:"description=Workspace-relative directory or file; default workspace root"`
	FilePattern   string `json:"file_pattern,omitempty" jsonschema:"description=Optional glob such as *.go or *.tsx"`
	Regex         bool   `json:"regex,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	MaxResults    int    `json:"max_results,omitempty" jsonschema:"description=Maximum matches; default 50 and hard limit 200"`
}

type workspaceSearchMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Preview string `json:"preview"`
}

type searchWorkspaceOutput struct {
	Matches   []workspaceSearchMatch `json:"matches"`
	Count     int                    `json:"count"`
	Truncated bool                   `json:"truncated"`
}

type replaceWorkspaceTextInput struct {
	Path       string `json:"path" jsonschema:"required,description=Workspace-relative file path"`
	OldText    string `json:"old_text" jsonschema:"required,description=Exact existing text to replace"`
	NewText    string `json:"new_text" jsonschema:"description=Replacement text; may be empty to delete old_text"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"description=Replace every exact occurrence; default false requires exactly one match"`
}

type workspaceMutationOutput struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements,omitempty"`
	Bytes        int    `json:"bytes"`
	Message      string `json:"message"`
}

type createWorkspaceFileInput struct {
	Path    string `json:"path" jsonschema:"required,description=New workspace-relative file path"`
	Content string `json:"content" jsonschema:"description=Complete initial file content"`
}

type getWorkspaceChangesInput struct {
	Path string `json:"path,omitempty" jsonschema:"description=Optional workspace-relative path filter"`
}

type getWorkspaceChangesOutput struct {
	Status    string `json:"status"`
	Diff      string `json:"diff"`
	Truncated bool   `json:"truncated"`
}

func registerWorkspaceCodeTools(r *Registry, workspaceRoot string) error {
	readTool, err := utils.InferTool("read_workspace_file",
		"Read a bounded line range from a text file inside the workspace. Use before editing; request additional ranges when needed.",
		func(_ context.Context, in readWorkspaceFileInput) (readWorkspaceFileOutput, error) {
			return readWorkspaceFile(workspaceRoot, in)
		})
	if err != nil {
		return err
	}
	r.AddTool(readTool)

	searchTool, err := utils.InferTool("search_workspace",
		"Search source files in the workspace with literal text or regex. Returns bounded path/line previews and skips generated dependency directories.",
		func(_ context.Context, in searchWorkspaceInput) (searchWorkspaceOutput, error) {
			return searchWorkspace(workspaceRoot, in)
		})
	if err != nil {
		return err
	}
	r.AddTool(searchTool)

	replaceTool, err := utils.InferTool("replace_workspace_text",
		"Precisely edit an existing workspace text file by replacing exact old_text. Read the file first. The call fails on stale or ambiguous text unless replace_all is true. Requires approval in balanced control mode.",
		func(_ context.Context, in replaceWorkspaceTextInput) (workspaceMutationOutput, error) {
			return replaceWorkspaceText(workspaceRoot, in)
		})
	if err != nil {
		return err
	}
	r.AddTool(replaceTool)

	createTool, err := utils.InferTool("create_workspace_file",
		"Create a new text file inside the workspace. It never overwrites an existing file. Requires approval in balanced control mode.",
		func(_ context.Context, in createWorkspaceFileInput) (workspaceMutationOutput, error) {
			return createWorkspaceFile(workspaceRoot, in)
		})
	if err != nil {
		return err
	}
	r.AddTool(createTool)

	changesTool, err := utils.InferTool("get_workspace_changes",
		"Return git status plus staged and unstaged textual diffs for the workspace. Use after edits to review scope before reporting completion.",
		func(ctx context.Context, in getWorkspaceChangesInput) (getWorkspaceChangesOutput, error) {
			return getWorkspaceChanges(ctx, workspaceRoot, in)
		})
	if err != nil {
		return err
	}
	r.AddTool(changesTool)
	return nil
}

func resolveWorkspacePath(root, input string, allowRoot, mustExist bool) (string, string, error) {
	rootAbs, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", "", err
	}
	rootAbs, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	rootInfo, err := os.Stat(rootAbs)
	if err != nil {
		return "", "", err
	}
	if !rootInfo.IsDir() {
		return "", "", fmt.Errorf("workspace root is not a directory")
	}
	raw := strings.TrimSpace(strings.ReplaceAll(input, "\\", "/"))
	if raw == "" && !allowRoot {
		return "", "", fmt.Errorf("workspace-relative path is required")
	}
	if filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return "", "", fmt.Errorf("absolute paths are not allowed: %q", input)
	}
	clean := filepath.Clean(filepath.FromSlash(raw))
	if clean == "." && !allowRoot {
		return "", "", fmt.Errorf("workspace root is not a file")
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path escapes workspace: %q", input)
	}
	full := filepath.Join(rootAbs, clean)
	if mustExist {
		evaluated, evalErr := filepath.EvalSymlinks(full)
		if evalErr != nil {
			return "", "", evalErr
		}
		full = evaluated
	} else {
		// Resolve the nearest existing parent so a newly created nested path
		// cannot escape through a symlinked ancestor.
		cursor := filepath.Dir(full)
		missing := make([]string, 0, 4)
		for {
			if _, statErr := os.Lstat(cursor); statErr == nil {
				break
			} else if !os.IsNotExist(statErr) {
				return "", "", statErr
			}
			parent := filepath.Dir(cursor)
			if parent == cursor {
				return "", "", fmt.Errorf("cannot resolve parent for %q", input)
			}
			missing = append([]string{filepath.Base(cursor)}, missing...)
			cursor = parent
		}
		evaluated, evalErr := filepath.EvalSymlinks(cursor)
		if evalErr != nil {
			return "", "", evalErr
		}
		for _, part := range missing {
			evaluated = filepath.Join(evaluated, part)
		}
		full = filepath.Join(evaluated, filepath.Base(full))
	}
	rel, err := filepath.Rel(rootAbs, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("path escapes workspace: %q", input)
	}
	return filepath.ToSlash(filepath.Clean(clean)), full, nil
}

func readWorkspaceFile(root string, in readWorkspaceFileInput) (readWorkspaceFileOutput, error) {
	clean, full, err := resolveWorkspacePath(root, in.Path, false, true)
	if err != nil {
		return readWorkspaceFileOutput{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return readWorkspaceFileOutput{}, err
	}
	if !info.Mode().IsRegular() {
		return readWorkspaceFileOutput{}, fmt.Errorf("path is not a regular file: %s", clean)
	}
	if info.Size() > maxAgentSourceBytes {
		return readWorkspaceFileOutput{}, fmt.Errorf("file exceeds %d MB source limit", maxAgentSourceBytes/(1024*1024))
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return readWorkspaceFileOutput{}, err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return readWorkspaceFileOutput{}, fmt.Errorf("binary file is not supported: %s", clean)
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	start := in.StartLine
	if start <= 0 {
		start = 1
	}
	end := in.EndLine
	if end <= 0 || end > start+399 {
		end = start + 399
	}
	if start > len(lines) {
		return readWorkspaceFileOutput{}, fmt.Errorf("start_line %d exceeds total lines %d", start, len(lines))
	}
	if end > len(lines) {
		end = len(lines)
	}
	var out strings.Builder
	for i := start; i <= end; i++ {
		out.WriteString(strconv.Itoa(i))
		out.WriteString(": ")
		out.WriteString(lines[i-1])
		if i < end {
			out.WriteByte('\n')
		}
	}
	return readWorkspaceFileOutput{
		Path: clean, Content: out.String(), StartLine: start, EndLine: end,
		TotalLines: len(lines), Truncated: end < len(lines),
	}, nil
}

func searchWorkspace(root string, in searchWorkspaceInput) (searchWorkspaceOutput, error) {
	query := in.Query
	if query == "" {
		return searchWorkspaceOutput{}, fmt.Errorf("query is required")
	}
	_, target, err := resolveWorkspacePath(root, in.Path, true, true)
	if err != nil {
		return searchWorkspaceOutput{}, err
	}
	limit := in.MaxResults
	if limit <= 0 {
		limit = 50
	}
	if limit > maxSearchResults {
		limit = maxSearchResults
	}
	pattern := query
	if !in.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !in.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return searchWorkspaceOutput{}, fmt.Errorf("invalid search expression: %w", err)
	}
	result := searchWorkspaceOutput{Matches: []workspaceSearchMatch{}}
	walkErr := filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != target && agentIgnoredDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if len(result.Matches) >= limit {
			result.Truncated = true
			return filepath.SkipAll
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxAgentSourceBytes {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if in.FilePattern != "" {
			matched, _ := filepath.Match(in.FilePattern, entry.Name())
			if !matched {
				matched, _ = filepath.Match(filepath.ToSlash(in.FilePattern), rel)
			}
			if !matched {
				return nil
			}
		}
		b, err := os.ReadFile(path)
		if err != nil || bytes.IndexByte(b, 0) >= 0 {
			return nil
		}
		for lineIndex, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			locs := re.FindAllStringIndex(line, -1)
			for _, loc := range locs {
				preview := strings.TrimSpace(line)
				previewRunes := []rune(preview)
				if len(previewRunes) > 300 {
					preview = string(previewRunes[:300]) + "…"
				}
				result.Matches = append(result.Matches, workspaceSearchMatch{
					Path: rel, Line: lineIndex + 1, Column: loc[0] + 1, Preview: preview,
				})
				if len(result.Matches) >= limit {
					result.Truncated = true
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if walkErr != nil {
		return searchWorkspaceOutput{}, walkErr
	}
	sort.SliceStable(result.Matches, func(i, j int) bool {
		if result.Matches[i].Path != result.Matches[j].Path {
			return result.Matches[i].Path < result.Matches[j].Path
		}
		return result.Matches[i].Line < result.Matches[j].Line
	})
	result.Count = len(result.Matches)
	return result, nil
}

func replaceWorkspaceText(root string, in replaceWorkspaceTextInput) (workspaceMutationOutput, error) {
	if in.OldText == "" {
		return workspaceMutationOutput{}, fmt.Errorf("old_text must not be empty")
	}
	clean, full, err := resolveWorkspacePath(root, in.Path, false, true)
	if err != nil {
		return workspaceMutationOutput{}, err
	}
	info, err := os.Stat(full)
	if err != nil {
		return workspaceMutationOutput{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxAgentSourceBytes {
		return workspaceMutationOutput{}, fmt.Errorf("file is not a supported source file: %s", clean)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return workspaceMutationOutput{}, err
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return workspaceMutationOutput{}, fmt.Errorf("binary file is not supported: %s", clean)
	}
	content := string(b)
	count := strings.Count(content, in.OldText)
	if count == 0 {
		return workspaceMutationOutput{}, fmt.Errorf("old_text was not found; reread the file before editing")
	}
	if !in.ReplaceAll && count != 1 {
		return workspaceMutationOutput{}, fmt.Errorf("old_text matched %d times; provide a larger unique block or set replace_all", count)
	}
	replacements := count
	if !in.ReplaceAll {
		replacements = 1
		content = strings.Replace(content, in.OldText, in.NewText, 1)
	} else {
		content = strings.ReplaceAll(content, in.OldText, in.NewText)
	}
	if len(content) > maxAgentSourceBytes {
		return workspaceMutationOutput{}, fmt.Errorf("edited file would exceed %d MB source limit", maxAgentSourceBytes/(1024*1024))
	}
	if err := os.WriteFile(full, []byte(content), info.Mode().Perm()); err != nil {
		return workspaceMutationOutput{}, err
	}
	return workspaceMutationOutput{Path: clean, Replacements: replacements, Bytes: len(content), Message: "exact replacement applied"}, nil
}

func createWorkspaceFile(root string, in createWorkspaceFileInput) (workspaceMutationOutput, error) {
	if len(in.Content) > maxAgentSourceBytes {
		return workspaceMutationOutput{}, fmt.Errorf("content exceeds %d MB source limit", maxAgentSourceBytes/(1024*1024))
	}
	clean, full, err := resolveWorkspacePath(root, in.Path, false, false)
	if err != nil {
		return workspaceMutationOutput{}, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return workspaceMutationOutput{}, err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return workspaceMutationOutput{}, err
	}
	if _, err := f.WriteString(in.Content); err != nil {
		_ = f.Close()
		_ = os.Remove(full)
		return workspaceMutationOutput{}, err
	}
	if err := f.Close(); err != nil {
		return workspaceMutationOutput{}, err
	}
	return workspaceMutationOutput{Path: clean, Bytes: len(in.Content), Message: "new file created"}, nil
}

func getWorkspaceChanges(ctx context.Context, root string, in getWorkspaceChangesInput) (getWorkspaceChangesOutput, error) {
	filter := ""
	if strings.TrimSpace(in.Path) != "" {
		clean, _, err := resolveWorkspacePath(root, in.Path, false, false)
		if err != nil {
			return getWorkspaceChangesOutput{}, err
		}
		filter = clean
	}
	argsFor := func(base ...string) []string {
		if filter == "" {
			return base
		}
		return append(base, "--", filter)
	}
	run := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
	status, err := run(argsFor("status", "--short")...)
	if err != nil {
		return getWorkspaceChangesOutput{}, err
	}
	unstaged, unstagedErr := run(argsFor("diff", "--no-color")...)
	staged, stagedErr := run(argsFor("diff", "--cached", "--no-color")...)
	if unstagedErr != nil && stagedErr != nil {
		return getWorkspaceChangesOutput{}, unstagedErr
	}
	diff := unstaged
	if strings.TrimSpace(staged) != "" {
		diff += "\n[staged changes]\n" + staged
	}
	truncated := len(diff) > maxGitOutputBytes
	if truncated {
		diff = diff[:maxGitOutputBytes] + "\n… diff truncated …"
	}
	return getWorkspaceChangesOutput{Status: status, Diff: diff, Truncated: truncated}, nil
}
