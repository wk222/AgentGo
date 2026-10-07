package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceCodeReadSearchReplaceCreate(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, "src")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sourceDir, "main.go")
	if err := os.WriteFile(file, []byte("package main\n\nfunc answer() int { return 41 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	read, err := readWorkspaceFile(root, readWorkspaceFileInput{Path: "src/main.go", StartLine: 2, EndLine: 3})
	if err != nil {
		t.Fatal(err)
	}
	if read.StartLine != 2 || read.EndLine != 3 || !strings.Contains(read.Content, "3: func answer") {
		t.Fatalf("unexpected read result: %+v", read)
	}

	search, err := searchWorkspace(root, searchWorkspaceInput{Query: "return 41", FilePattern: "*.go"})
	if err != nil {
		t.Fatal(err)
	}
	if search.Count != 1 || search.Matches[0].Path != "src/main.go" || search.Matches[0].Line != 3 {
		t.Fatalf("unexpected search result: %+v", search)
	}

	changed, err := replaceWorkspaceText(root, replaceWorkspaceTextInput{
		Path: "src/main.go", OldText: "return 41", NewText: "return 42",
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Replacements != 1 {
		t.Fatalf("unexpected mutation result: %+v", changed)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "return 42") {
		t.Fatalf("replacement not persisted: %s", b)
	}

	created, err := createWorkspaceFile(root, createWorkspaceFileInput{Path: "internal/new.txt", Content: "new"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Path != "internal/new.txt" {
		t.Fatalf("unexpected created path: %+v", created)
	}
	if _, err := createWorkspaceFile(root, createWorkspaceFileInput{Path: "internal/new.txt", Content: "overwrite"}); err == nil {
		t.Fatal("create should refuse to overwrite an existing file")
	}
}

func TestReplaceWorkspaceTextRejectsStaleAndAmbiguousContent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "values.txt")
	if err := os.WriteFile(path, []byte("same\nsame\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := replaceWorkspaceText(root, replaceWorkspaceTextInput{Path: "values.txt", OldText: "missing", NewText: "x"}); err == nil {
		t.Fatal("stale old_text should fail")
	}
	if _, err := replaceWorkspaceText(root, replaceWorkspaceTextInput{Path: "values.txt", OldText: "same", NewText: "x"}); err == nil {
		t.Fatal("ambiguous old_text should fail without replace_all")
	}
}

func TestWorkspacePathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, _, err := resolveWorkspacePath(root, "../../outside.txt", false, false); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	if _, _, err := resolveWorkspacePath(root, filepath.Join(root, "absolute.txt"), false, false); err == nil {
		t.Fatal("expected absolute path to be rejected")
	}
}

func TestWorkspacePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, _, err := resolveWorkspacePath(root, "link/nested/new.txt", false, false); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
}
