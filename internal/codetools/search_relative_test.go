package codetools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentgo/internal/coderuntime"
)

// The model writes relative paths (".", "src", "pkg/*.go") far more often than
// absolute ones. Those must be resolved against the workspace root, not the
// process working directory: the CLI can be started anywhere, and the desktop
// app's cwd has nothing to do with the folder the user opened. code_ls already
// works that way; grep and glob have to agree with it.

// relativeWorkdir builds a workspace in a temp dir and returns it; the tests
// stay out of that directory so the process cwd cannot mask the bug.
func relativeWorkdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"main.go":     "package main\n// needle here\n",
		"pkg/util.go": "package pkg\n// needle again\n",
		"docs/a.md":   "needle in docs\n",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestGrepRelativePathResolvesToWorkDir(t *testing.T) {
	dir := relativeWorkdir(t)
	s := &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Rg: func() string { return "" }})}

	for _, p := range []string{".", "pkg", "./pkg"} {
		out := s.grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: p})
		if out.IsError {
			t.Fatalf("path %q: %s", p, out.Content)
		}
		if !strings.Contains(out.Content, "needle") {
			t.Fatalf("path %q grepped outside the workspace: %q", p, out.Content)
		}
	}

	// A relative path with no argument at all must mean the workspace too.
	out := s.grep(context.Background(), "", "", grepArgs{Pattern: "needle"})
	if !strings.Contains(out.Content, "needle") {
		t.Fatalf("default path grepped outside the workspace: %q", out.Content)
	}
}

func TestGlobRelativePathResolvesToWorkDir(t *testing.T) {
	dir := relativeWorkdir(t)
	s := &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Rg: func() string { return "" }})}

	out := s.glob(context.Background(), "", "", globArgs{Pattern: "*.go", Path: "."})
	if out.IsError {
		t.Fatalf("%s", out.Content)
	}
	if !strings.Contains(out.Content, "main.go") {
		t.Fatalf("glob with path \".\" did not find the workspace files: %q", out.Content)
	}

	out = s.glob(context.Background(), "", "", globArgs{Pattern: "*.go"})
	if !strings.Contains(out.Content, "main.go") {
		t.Fatalf("glob with no path did not find the workspace files: %q", out.Content)
	}

	// A literal directory prefix in the pattern is still relative to the workspace.
	out = s.glob(context.Background(), "", "", globArgs{Pattern: "pkg/*.go", Path: "."})
	if !strings.Contains(out.Content, "util.go") {
		t.Fatalf("glob pkg/*.go: %q", out.Content)
	}
}

// Absolute paths keep working, and the three tools must report the same root:
// ls prints it, grep and glob must search it.
func TestSearchToolsAgreeOnRoot(t *testing.T) {
	dir := relativeWorkdir(t)
	s := &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Rg: func() string { return "" }})}

	lsOut := s.ls(context.Background(), "", "", lsArgs{Path: "."})
	if lsOut.IsError {
		t.Fatalf("ls: %s", lsOut.Content)
	}
	grepOut := s.grep(context.Background(), "", "", grepArgs{Pattern: "needle", Path: "."})
	globOut := s.glob(context.Background(), "", "", globArgs{Pattern: "*.go", Path: "."})

	for _, got := range []coderuntime.ToolResult{grepOut, globOut} {
		if got.IsError {
			t.Fatalf("%s", got.Content)
		}
		if strings.Contains(got.Content, "No files found") {
			t.Fatalf("a tool searched a different tree than ls: %q", got.Content)
		}
	}
}

// Reading outside the workspace is what the approval gate is for. code_ls has
// always asked; grep and glob used to read any path on the machine without
// asking, even with code-tool actions switched off.
func TestSearchToolsAskBeforeLeavingWorkDir(t *testing.T) {
	dir := relativeWorkdir(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside-secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var asked []string
	deny := func(_ context.Context, r coderuntime.FSRequest) bool {
		asked = append(asked, r.Tool+" "+r.Path)
		return false
	}
	s := &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Approve: deny, Rg: func() string { return "" }})}

	lsOut := s.ls(context.Background(), "s", "c", lsArgs{Path: outside})
	if !lsOut.IsError || !strings.Contains(lsOut.Content, "denied") {
		t.Fatalf("ls outside the workspace: %q", lsOut.Content)
	}
	grepOut := s.grep(context.Background(), "s", "c", grepArgs{Pattern: "outside-secret", Path: outside})
	if !grepOut.IsError || strings.Contains(grepOut.Content, "outside-secret") {
		t.Fatalf("grep read outside the workspace without approval: %q", grepOut.Content)
	}
	globOut := s.glob(context.Background(), "s", "c", globArgs{Pattern: "*.txt", Path: outside})
	if !globOut.IsError || strings.Contains(globOut.Content, "secret.txt") {
		t.Fatalf("glob listed outside the workspace without approval: %q", globOut.Content)
	}
	for _, want := range []string{"grep", "glob", "ls"} {
		found := false
		for _, a := range asked {
			if strings.HasPrefix(a, want+" ") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s did not ask before leaving the workspace; asked for %v", want, asked)
		}
	}

	// A path inside the workspace must not trigger the gate at all.
	asked = nil
	if out := s.grep(context.Background(), "s", "c", grepArgs{Pattern: "needle", Path: "."}); out.IsError {
		t.Fatalf("grep inside the workspace: %s", out.Content)
	}
	if len(asked) != 0 {
		t.Fatalf("grep asked about a path inside the workspace: %v", asked)
	}
}
