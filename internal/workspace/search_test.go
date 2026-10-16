package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSearch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.go", "package a\nfunc Hello() {}\nfunc hello2() {}\n")
	writeFile(t, root, "sub/b.txt", "no match here\n")
	writeFile(t, root, "node_modules/x.js", "Hello ignored\n")
	writeFile(t, root, "bin.dat", "Hello\x00binary")

	res, total, err := Search(root, SearchOptions{Query: "hello"})
	if err != nil || total != 2 || len(res) != 1 || res[0].FilePath != "a.go" {
		t.Fatalf("case-insensitive: res=%+v total=%d err=%v", res, total, err)
	}
	res, total, _ = Search(root, SearchOptions{Query: "hello", MatchCase: true})
	if total != 1 || res[0].Matches[0].Line != 3 {
		t.Fatalf("match case: res=%+v total=%d", res, total)
	}
	_, total, _ = Search(root, SearchOptions{Query: "Hello", MatchCase: true, WholeWord: true, Exclude: "*.go"})
	if total != 0 {
		t.Fatalf("exclude *.go should hide a.go, got %d", total)
	}
	if _, _, err := Search(root, SearchOptions{Query: "(", IsRegex: true}); err == nil {
		t.Fatal("invalid regex must error")
	}
	if res, total, err := Search(root, SearchOptions{Query: "  "}); err != nil || total != 0 || len(res) != 0 {
		t.Fatalf("empty query: %v %d %v", res, total, err)
	}
}

func TestReadMedia(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "icon.svg", "<svg/>")
	m, err := ReadMedia(root, "icon.svg")
	if err != nil || m.Mime != "image/svg+xml" || m.Size != 6 || m.DataURL == "" {
		t.Fatalf("media=%+v err=%v", m, err)
	}
	if _, err := ReadMedia(root, "../escape"); err == nil {
		t.Fatal("path escape must fail")
	}
}

func TestGitStatusAndBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if _, err := RunGit(root, args...); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, root, "tracked.txt", "v1")
	if _, err := GitStage(root, "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := GitCommit(root, "init"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "tracked.txt", "v2")
	writeFile(t, root, "new.txt", "x")

	st, err := GitStatus(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Unstaged) != 1 || len(st.Untracked) != 1 || st.Total() != 2 {
		t.Fatalf("status=%+v", st)
	}
	if _, err := GitStage(root, ""); err != nil {
		t.Fatal(err)
	}
	if st, _ = GitStatus(root); len(st.Staged) != 2 {
		t.Fatalf("after stage all: %+v", st)
	}
	if _, err := GitUnstage(root, ""); err != nil {
		t.Fatal(err)
	}
	if st, _ = GitStatus(root); len(st.Staged) != 0 {
		t.Fatalf("after unstage all: %+v", st)
	}
	branches, cur, err := GitBranches(root)
	if err != nil || cur == "" || len(branches) != 1 {
		t.Fatalf("branches=%v cur=%q err=%v", branches, cur, err)
	}
}
