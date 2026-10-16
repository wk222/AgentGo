package workspace

import (
	"path/filepath"
	"strings"
)

// GitItem is one entry of `git status`.
type GitItem struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Staged bool   `json:"staged"`
}

// GitState is the parsed result of `git status --porcelain=v1 -b`.
type GitState struct {
	Branch    string
	Staged    []GitItem
	Unstaged  []GitItem
	Untracked []GitItem
}

// Total is the number of changed paths across the three groups.
func (g GitState) Total() int { return len(g.Staged) + len(g.Unstaged) + len(g.Untracked) }

// GitStatus parses the working-tree status of root.
func GitStatus(root string) (GitState, error) {
	out, err := RunGit(root, "status", "--porcelain=v1", "-b")
	if err != nil {
		return GitState{}, err
	}
	st := GitState{Branch: "main"}
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if i == 0 && strings.HasPrefix(line, "## ") {
			b := strings.TrimPrefix(line, "## ")
			if idx := strings.Index(b, "..."); idx >= 0 {
				b = b[:idx]
			}
			st.Branch = strings.TrimSpace(b)
			continue
		}
		if len(line) < 4 {
			continue
		}
		x, y := line[0], line[1] // index / worktree status
		rel := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(rel, " -> "); idx >= 0 {
			rel = strings.TrimSpace(rel[idx+4:])
		}
		rel = strings.Trim(rel, "\"")
		name := filepath.Base(rel)

		if x == '?' && y == '?' {
			st.Untracked = append(st.Untracked, GitItem{Path: rel, Name: name, Status: "??"})
			continue
		}
		if x != ' ' && x != '?' {
			st.Staged = append(st.Staged, GitItem{Path: rel, Name: name, Status: string(x), Staged: true})
		}
		if y != ' ' && y != '?' {
			st.Unstaged = append(st.Unstaged, GitItem{Path: rel, Name: name, Status: string(y)})
		}
	}
	return st, nil
}

func isAllPaths(rel string) bool {
	rel = strings.TrimSpace(rel)
	return rel == "" || rel == "."
}

// GitStage stages one path, or everything when rel is empty.
func GitStage(root, rel string) (string, error) {
	if isAllPaths(rel) {
		return RunGit(root, "add", "-A")
	}
	return RunGit(root, "add", "--", strings.TrimSpace(rel))
}

// GitUnstage unstages one path, or everything when rel is empty.
func GitUnstage(root, rel string) (string, error) {
	if isAllPaths(rel) {
		out, err := RunGit(root, "restore", "--staged", ".")
		if err != nil {
			out, err = RunGit(root, "reset", "HEAD", ".")
		}
		return out, err
	}
	clean := strings.TrimSpace(rel)
	out, err := RunGit(root, "restore", "--staged", "--", clean)
	if err != nil {
		out, err = RunGit(root, "reset", "HEAD", "--", clean)
	}
	return out, err
}

// GitCommit commits the index. The caller validates a non-empty message.
func GitCommit(root, message string) (string, error) {
	return RunGit(root, "commit", "-m", strings.TrimSpace(message))
}

// GitBranches lists local branches and the current one.
func GitBranches(root string) (branches []string, current string, err error) {
	out, err := RunGit(root, "branch", "--list", "--no-color")
	if err != nil {
		return nil, "", err
	}
	current = "main"
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if b, ok := strings.CutPrefix(l, "* "); ok {
			current = b
			l = b
		}
		branches = append(branches, l)
	}
	return branches, current, nil
}

// GitCheckout switches branch.
func GitCheckout(root, branch string) (string, error) {
	return RunGit(root, "checkout", strings.TrimSpace(branch))
}
