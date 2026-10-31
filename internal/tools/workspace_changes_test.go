package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// gitRepo makes a repository with one commit (a.txt and 中文.txt) in a temp
// directory, isolated from the user's global git configuration.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	writeFile(t, dir, "a.txt", "one\ntwo\n")
	writeFile(t, dir, "中文.txt", "你好\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceChangesShowsStatStagedAndReadableNames(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.txt", "one\ntwo\nthree\n") // unstaged
	writeFile(t, dir, "中文.txt", "你好\n世界\n")         // staged
	runGit(t, dir, "add", "中文.txt")
	writeFile(t, dir, "new.txt", "brand new\n") // untracked

	out, err := getWorkspaceChanges(context.Background(), dir, getWorkspaceChangesInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"M a.txt", "M  中文.txt", "?? new.txt"} {
		if !strings.Contains(out.Status, want) {
			t.Errorf("status lacks %q:\n%s", want, out.Status)
		}
	}
	if strings.Contains(out.Status, `\346`) {
		t.Errorf("non-ASCII names must not be escaped:\n%s", out.Status)
	}
	if !strings.Contains(out.Stat, "a.txt") || !strings.Contains(out.Stat, "中文.txt") || !strings.Contains(out.Stat, "[staged changes]") {
		t.Errorf("stat should cover unstaged and staged files:\n%s", out.Stat)
	}
	if !strings.Contains(out.Diff, "+three") || !strings.Contains(out.Diff, "+世界") || !strings.Contains(out.Diff, "[staged changes]") {
		t.Errorf("diff lacks the edits:\n%s", out.Diff)
	}
	if strings.Contains(out.Diff, "brand new") {
		t.Errorf("untracked content is not part of the diff; the reviewer reads it with the file tools:\n%s", out.Diff)
	}
	if out.Truncated {
		t.Error("a small diff must not be reported as truncated")
	}
}

func TestWorkspaceChangesPathFilterNarrowsEverything(t *testing.T) {
	dir := gitRepo(t)
	writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")
	writeFile(t, dir, "中文.txt", "你好\n世界\n")

	out, err := getWorkspaceChanges(context.Background(), dir, getWorkspaceChangesInput{Path: "a.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Diff, "+three") || strings.Contains(out.Diff, "世界") {
		t.Errorf("diff is not limited to a.txt:\n%s", out.Diff)
	}
	if strings.Contains(out.Status, "中文") || strings.Contains(out.Stat, "中文") {
		t.Errorf("status/stat are not limited to a.txt:\n%s\n%s", out.Status, out.Stat)
	}
}

// With core.autocrlf=true git warns on stderr that LF will be replaced by CRLF.
// That warning once ended up inside the diff the model had to review.
func TestWorkspaceChangesKeepsGitWarningsOutOfTheDiff(t *testing.T) {
	dir := gitRepo(t)
	runGit(t, dir, "config", "core.autocrlf", "true")
	writeFile(t, dir, "a.txt", "one\ntwo\nthree\n")

	out, err := getWorkspaceChanges(context.Background(), dir, getWorkspaceChangesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.Diff, "warning:") || strings.Contains(out.Stat, "warning:") || strings.Contains(out.Status, "warning:") {
		t.Errorf("a git warning leaked into the output:\nstatus=%s\nstat=%s\ndiff=%s", out.Status, out.Stat, out.Diff)
	}
	if !strings.Contains(out.Diff, "three") {
		t.Errorf("the diff itself is missing:\n%s", out.Diff)
	}
}

func TestWorkspaceChangesCutsAtALineAndSaysHowToGetTheRest(t *testing.T) {
	dir := gitRepo(t)
	var b strings.Builder
	for b.Len() < 3*maxGitOutputBytes {
		b.WriteString("这是很长的一行，用来把 diff 撑过上限。\n")
	}
	writeFile(t, dir, "a.txt", b.String())

	out, err := getWorkspaceChanges(context.Background(), dir, getWorkspaceChangesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Fatal("a diff over the limit must be reported as truncated")
	}
	if len(out.Diff) > maxGitOutputBytes+len(truncatedNote) {
		t.Errorf("diff is %d bytes, over the %d limit", len(out.Diff), maxGitOutputBytes)
	}
	if !utf8.ValidString(out.Diff) {
		t.Error("the cut split a UTF-8 character")
	}
	if !strings.HasSuffix(out.Diff, truncatedNote) || !strings.Contains(out.Diff, "path") {
		t.Errorf("the model is not told how to read the rest:\n…%s", out.Diff[len(out.Diff)-200:])
	}
	body := strings.TrimSuffix(out.Diff, truncatedNote)
	if !strings.HasSuffix(body, "。") {
		t.Errorf("the cut should fall at the end of a line, got …%q", body[len(body)-30:])
	}
}

func TestWorkspaceChangesOutsideARepositoryIsAnError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir)) // never find a parent repository
	if _, err := getWorkspaceChanges(context.Background(), dir, getWorkspaceChangesInput{}); err == nil {
		t.Fatal("expected an error outside a git repository")
	} else if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("the error should say why: %v", err)
	}
}
