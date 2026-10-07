package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiffTreesFindsModifiedAndCreatedTextFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.txt"), "one\n")
	write(t, filepath.Join(root, "same.txt"), "same\n")
	write(t, filepath.Join(root, ".git", "HEAD"), "ref\n")
	write(t, filepath.Join(root, "bin.dat"), "x\x00y")
	before := scanTree(root)

	write(t, filepath.Join(root, "a.txt"), "two\n")
	write(t, filepath.Join(root, "new.txt"), "fresh\n")
	write(t, filepath.Join(root, ".git", "HEAD"), "changed\n") // skipped dir
	write(t, filepath.Join(root, "bin.dat"), "z\x00w")         // binary: no honest diff
	got := map[string]fileDelta{}
	for _, d := range diffTrees(before, scanTree(root)) {
		got[filepath.Base(d.path)] = d
	}
	if len(got) != 2 {
		t.Fatalf("deltas = %+v", got)
	}
	if d := got["a.txt"]; d.before != "one\n" || d.after != "two\n" {
		t.Fatalf("a.txt delta = %+v", d)
	}
	if d := got["new.txt"]; d.before != "" || d.after != "fresh\n" {
		t.Fatalf("new.txt delta = %+v", d)
	}
}

func TestDiffTreesSkipsFilesWithoutCapturedBefore(t *testing.T) {
	root := t.TempDir()
	big := filepath.Join(root, "big.txt")
	write(t, big, strings.Repeat("a", treeMaxFile+1)) // too large to snapshot
	before := scanTree(root)
	write(t, big, strings.Repeat("b", treeMaxFile+1))
	if d := diffTrees(before, scanTree(root)); len(d) != 0 {
		t.Fatalf("diff without a captured before-image: %+v", d)
	}
}

func TestShellCommandChangesPopulateModifiedFiles(t *testing.T) {
	var path string
	_, files := runTurnFiles(t, func(c *crushTurn, dir string) {
		path = filepath.Join(dir, "s.txt")
		write(t, path, "before\n")
		c.toolCall("b1", shellTool, `{"command":"sed -i ..."}`)
		write(t, path, "after\n") // the command runs
		c.toolResult("b1", shellTool, "", false)
	})
	if len(files) != 2 || files[0].Content != "before\n" || files[1].Content != "after\n" || files[0].Path != path {
		t.Fatalf("files = %+v", files)
	}
}
