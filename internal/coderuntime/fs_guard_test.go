package coderuntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fakeGitHubToken = "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789" // 36 chars after the prefix

func run(t *testing.T, tl Tool, args map[string]any) ToolResult {
	t.Helper()
	r, err := tl.Run(context.Background(), "s", "c", fsArgs(args))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewSecretsIntroducedAllowsPreservingExisting(t *testing.T) {
	old := "api_key = " + fakeGitHubToken + "\n"
	if got := newSecretsIntroduced(old, old+"theme = dark\n"); got != "" {
		t.Fatalf("a carried-through secret must be allowed, got %q", got)
	}
	if got := newSecretsIntroduced(old, "api_key = "+strings.Replace(fakeGitHubToken, "abc", "xyz", 1)+"\n"); got == "" {
		t.Fatal("a changed secret must be flagged")
	}
	if got := newSecretsIntroduced("", "-----BEGIN RSA PRIVATE KEY-----\nMIIE"); got != "private key block" {
		t.Fatalf("got %q", got)
	}
	if got := newSecretsIntroduced("", "AKIAIOSFODNN7EXAMPLE"); got != "" {
		t.Fatalf("docs example access key id must not trip: %q", got)
	}
}

func TestWriteRefusesNewSecretButNotPlainText(t *testing.T) {
	dir := t.TempDir()
	tools := NewFSTools(FSOptions{WorkDir: dir})
	write := fsTool(t, tools, "write")
	p := filepath.Join(dir, "cfg.env")

	r := run(t, write, map[string]any{"file_path": p, "content": "TOKEN=" + fakeGitHubToken + "\n"})
	if !r.IsError || !strings.Contains(r.Content, "GitHub token") {
		t.Fatalf("expected a refusal naming the secret, got %+v", r)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("the refused write must not create the file")
	}
	if r := run(t, write, map[string]any{"file_path": p, "content": "TOKEN=${GITHUB_TOKEN}\n"}); r.IsError {
		t.Fatalf("placeholder should be fine: %+v", r)
	}
}

func TestEditRefusesToIntroduceSecret(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("token=PLACEHOLDER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := viewed(t, FSOptions{WorkDir: dir}, p)
	r := run(t, fsTool(t, tools, "edit"), map[string]any{"file_path": p, "old_string": "PLACEHOLDER", "new_string": fakeGitHubToken})
	if !r.IsError {
		t.Fatalf("edit must refuse: %+v", r)
	}
	if b, _ := os.ReadFile(p); !strings.Contains(string(b), "PLACEHOLDER") {
		t.Fatal("file must be untouched")
	}
}

func TestSecretGuardCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	tools := NewFSTools(FSOptions{WorkDir: dir, DisableSecretGuard: true})
	r := run(t, fsTool(t, tools, "write"), map[string]any{"file_path": filepath.Join(dir, "k.txt"), "content": fakeGitHubToken})
	if r.IsError {
		t.Fatalf("guard disabled: %+v", r)
	}
	t.Setenv("AGENTGO_ALLOW_SECRET_WRITES", "1")
	tools = NewFSTools(FSOptions{WorkDir: dir})
	r = run(t, fsTool(t, tools, "write"), map[string]any{"file_path": filepath.Join(dir, "k2.txt"), "content": fakeGitHubToken})
	if r.IsError {
		t.Fatalf("env opt-out: %+v", r)
	}
}

func TestOverwriteKeepsABackupOfThePreviousBytes(t *testing.T) {
	dir := t.TempDir() // not a git repo, so nothing can restore the file
	p := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(p, []byte("precious v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := viewed(t, FSOptions{WorkDir: dir}, p)

	r := run(t, fsTool(t, tools, "write"), map[string]any{"file_path": p, "content": "v2\n"})
	if r.IsError || !strings.Contains(r.Content, "previous version saved to") {
		t.Fatalf("write result should point at the backup: %+v", r)
	}
	files, _ := filepath.Glob(filepath.Join(dir, ".agentgo", "overwrite-backups", "*-notes.txt"))
	if len(files) != 1 {
		t.Fatalf("want exactly one backup, got %v", files)
	}
	if b, _ := os.ReadFile(files[0]); string(b) != "precious v1\n" {
		t.Fatalf("backup content = %q", b)
	}

	// An edit backs up the version it replaced as well.
	r = run(t, fsTool(t, tools, "edit"), map[string]any{"file_path": p, "old_string": "v2", "new_string": "v3"})
	if r.IsError || !strings.Contains(r.Content, "previous version saved to") {
		t.Fatalf("edit result should mention the backup: %+v", r)
	}
}

func TestBackupSkippedForNewFilesAndWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	tools := NewFSTools(FSOptions{WorkDir: dir})
	r := run(t, fsTool(t, tools, "write"), map[string]any{"file_path": filepath.Join(dir, "new.txt"), "content": "hi\n"})
	if r.IsError || strings.Contains(r.Content, "previous version") {
		t.Fatalf("a create has nothing to back up: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agentgo")); err == nil {
		t.Fatal("no backup dir should be created for a create")
	}

	p := filepath.Join(dir, "old.txt")
	_ = os.WriteFile(p, []byte("x\n"), 0o644)
	tools = viewed(t, FSOptions{WorkDir: dir, DisableBackup: true}, p)
	r = run(t, fsTool(t, tools, "write"), map[string]any{"file_path": p, "content": "y\n"})
	if r.IsError || strings.Contains(r.Content, "previous version") {
		t.Fatalf("backup disabled: %+v", r)
	}
}

func TestPruneBackupsKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxBackupFiles+5; i++ {
		name := filepath.Join(dir, strings.Repeat("0", 4)+string(rune('a'+i/26))+string(rune('a'+i%26)))
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneBackups(dir)
	entries, _ := os.ReadDir(dir)
	if len(entries) != maxBackupFiles {
		t.Fatalf("got %d entries, want %d", len(entries), maxBackupFiles)
	}
}
