package bridge

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

var defaultWorkspaceIgnores = map[string]bool{
	".git":          true,
	".next":         true,
	".nuxt":         true,
	".pytest_cache": true,
	".ruff_cache":   true,
	".venv":         true,
	"__pycache__":   true,
	"build":         true,
	"coverage":      true,
	"dist":          true,
	"node_modules":  true,
	"target":        true,
	"vendor":        true,
	"wandb":         true,
	".local":        true,
	".task":         true,
	".turbo":        true,
	".cache":        true,
	".mypy_cache":   true,
	".tox":          true,
	".idea":         true,
	".vscode":       true,
	"__snapshots__": true,
	"checkpoints":   true,
	"runs":          true,
	"tmp":           true,
	"temp":          true,
}

func shouldSkipWorkspaceEntry(root, dirRel, name string, isDir bool) bool {
	if strings.HasPrefix(name, ".") && name != ".cursor" && name != ".agentgoignore" {
		return true
	}
	if isDir && defaultWorkspaceIgnores[name] {
		return true
	}
	rel := filepath.ToSlash(filepath.Join(dirRel, name))
	for _, pattern := range loadAgentGoIgnore(root) {
		if ignorePatternMatches(pattern, rel, name, isDir) {
			return true
		}
	}
	return false
}

func loadAgentGoIgnore(root string) []string {
	b, err := os.ReadFile(filepath.Join(root, ".agentgoignore"))
	if err != nil {
		return nil
	}
	var patterns []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(filepath.ToSlash(line), "/")
		patterns = append(patterns, line)
	}
	return patterns
}

func ignorePatternMatches(pattern, rel, name string, isDir bool) bool {
	if pattern == "" {
		return false
	}
	if strings.HasSuffix(pattern, "/") {
		prefix := strings.TrimSuffix(pattern, "/")
		return isDir && (rel == prefix || strings.HasPrefix(rel, prefix+"/"))
	}
	if ok, _ := path.Match(pattern, rel); ok {
		return true
	}
	if ok, _ := path.Match(pattern, name); ok {
		return true
	}
	return rel == pattern || strings.HasPrefix(rel, pattern+"/")
}

func workspaceGitStatuses(root string) map[string]string {
	out, err := runGit(root, "status", "--porcelain")
	if err != nil || strings.TrimSpace(out) == "" {
		return nil
	}
	statuses := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		rel := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(rel, " -> "); idx >= 0 {
			rel = strings.TrimSpace(rel[idx+4:])
		}
		rel = strings.Trim(rel, "\"")
		if rel != "" {
			statuses[filepath.ToSlash(rel)] = status
		}
	}
	return statuses
}

func gitStatusForEntry(statuses map[string]string, rel string, isDir bool) string {
	if len(statuses) == 0 {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if status := statuses[rel]; status != "" {
		return status
	}
	if !isDir {
		return ""
	}
	prefix := strings.TrimSuffix(rel, "/") + "/"
	for p := range statuses {
		if strings.HasPrefix(p, prefix) {
			return "dirty"
		}
	}
	return ""
}
