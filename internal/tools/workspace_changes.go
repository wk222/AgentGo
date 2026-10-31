package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"

	"agentgo/internal/processutil"
)

const (
	maxGitOutputBytes = 64 * 1024
	maxGitStatBytes   = 16 * 1024
	truncatedNote     = "\n… truncated; call get_workspace_changes again with path set to one file to read the rest …"
)

const getWorkspaceChangesDescription = "Return git status, a per-file change summary (stat) and the staged and unstaged textual diffs of the workspace. " +
	"New untracked files show in status as '??' and are not part of the diff; their content is in 'untracked' as numbered lines " +
	"(text files only, size-limited: a file that is shown only in part, or not at all because it is binary or looks like a secret, says so, and you read it with the file tools). " +
	"The diff is cut at 64 KiB; when it is, call again with path set to one file to read that file's diff. " +
	"Use it after edits to check the scope before reporting completion, and as the first step of a code review."

type getWorkspaceChangesInput struct {
	Path string `json:"path,omitempty" jsonschema:"description=Optional workspace-relative path filter; use it to read one file's diff when the full diff is truncated"`
}

type getWorkspaceChangesOutput struct {
	Status    string `json:"status"`
	Stat      string `json:"stat,omitempty"`
	Diff      string `json:"diff"`
	Untracked string `json:"untracked,omitempty"`
	Truncated bool   `json:"truncated"`
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
	git := func(args ...string) (string, error) {
		// core.quotepath=false keeps non-ASCII file names readable instead of "\346\226\207...".
		full := append([]string{"-c", "core.quotepath=false"}, args...)
		if filter != "" {
			full = append(full, "--", filter)
		}
		var stdout, stderr bytes.Buffer
		cmd := exec.CommandContext(ctx, "git", full...)
		processutil.HideWindow(cmd)
		cmd.Dir = root
		// Only stdout is content. Git's warnings (for example about line endings
		// under core.autocrlf) go to stderr and must not end up inside the diff.
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
		}
		return stdout.String(), nil
	}

	status, err := git("status", "--short")
	if err != nil {
		return getWorkspaceChangesOutput{}, err
	}
	unstaged, unstagedErr := git("diff", "--no-color")
	staged, stagedErr := git("diff", "--cached", "--no-color")
	if unstagedErr != nil && stagedErr != nil {
		return getWorkspaceChangesOutput{}, unstagedErr
	}
	// The summary is a convenience for planning a per-file read; losing it is not an error.
	unstagedStat, _ := git("diff", "--stat=160,120", "--no-color")
	stagedStat, _ := git("diff", "--cached", "--stat=160,120", "--no-color")

	diff, truncated := cutAtLine(withStaged(unstaged, staged), maxGitOutputBytes)
	stat, _ := cutAtLine(withStaged(unstagedStat, stagedStat), maxGitStatBytes)

	// New files are not in any diff. Their content comes with the answer so that a
	// review does not depend on the model fetching each of them; failing to list
	// them is not an error, the status still names them.
	untracked := ""
	if listing, err := git("ls-files", "--others", "--exclude-standard", "-z"); err == nil && listing != "" {
		untracked = untrackedContent(root, strings.Split(strings.TrimRight(listing, "\x00"), "\x00"))
	}
	return getWorkspaceChangesOutput{Status: status, Stat: stat, Diff: diff, Untracked: untracked, Truncated: truncated}, nil
}

// withStaged appends the staged part after the unstaged one, under a marker
// line; an empty staged part adds nothing.
func withStaged(unstaged, staged string) string {
	if strings.TrimSpace(staged) == "" {
		return unstaged
	}
	return unstaged + "\n[staged changes]\n" + staged
}

// cutAtLine limits s to about limit bytes without splitting a UTF-8 character,
// and preferably at the end of a line. It reports whether it cut anything.
func cutAtLine(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	return cutPrefix(s, limit) + truncatedNote, true
}

// cutPrefix returns the start of s, which is longer than limit bytes: whole
// lines where possible, never a split UTF-8 character.
func cutPrefix(s string, limit int) string {
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	if i := strings.LastIndexByte(s[:cut], '\n'); i > cut/2 {
		cut = i
	}
	return s[:cut]
}
