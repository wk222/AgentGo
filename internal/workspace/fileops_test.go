package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileOpsRoundTrip(t *testing.T) {
	root := t.TempDir()

	if p, err := WriteText(root, "dir/a.txt", "hello"); err != nil || p != "dir/a.txt" {
		t.Fatalf("write: %q %v", p, err)
	}
	f, err := ReadText(root, "dir/a.txt")
	if err != nil || f.Content != "hello" || f.Size != 5 {
		t.Fatalf("read: %+v %v", f, err)
	}
	if _, err := CreateFile(root, "dir/a.txt"); err == nil {
		t.Fatal("CreateFile must not overwrite")
	}
	if _, err := CreateDir(root, "dir/sub"); err != nil {
		t.Fatal(err)
	}
	if p, err := Rename(root, "dir/a.txt", "dir/b.txt"); err != nil || p != "dir/b.txt" {
		t.Fatalf("rename: %q %v", p, err)
	}

	list := List(root, "dir")
	if len(list) != 2 || !list[0].IsDir || list[1].Name != "b.txt" {
		t.Fatalf("dirs first, then files: %+v", list)
	}

	if _, err := Delete(root, "dir"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "dir")); !os.IsNotExist(err) {
		t.Fatal("dir should be gone")
	}
}

func TestFileOpsRejectEscapeAndLargeFiles(t *testing.T) {
	root := t.TempDir()
	// ".." segments are folded under root (never resolved above it).
	clean, err := WriteText(root, "../outside.txt", "x")
	if err != nil || clean != "outside.txt" {
		t.Fatalf("write: %q %v", clean, err)
	}
	if _, err := os.Stat(filepath.Join(root, "outside.txt")); err != nil {
		t.Fatal("file must land inside root")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "outside.txt")); err == nil {
		t.Fatal("file must not escape root")
	}
	big := strings.Repeat("a", int(MaxPreviewBytes)+1)
	if err := os.WriteFile(filepath.Join(root, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ReadText(root, "big.txt")
	if !errors.Is(err, ErrTooLarge) || f.Path != "big.txt" || f.Size != MaxPreviewBytes+1 {
		t.Fatalf("too large: %+v %v", f, err)
	}
}

func TestRunShell(t *testing.T) {
	cmd := "echo hi"
	r := RunShell(t.TempDir(), cmd)
	if r.Err != nil || !strings.Contains(r.Stdout, "hi") || r.ExitCode != 0 {
		t.Fatalf("%+v", r)
	}
	fail := "exit 3"
	if runtime.GOOS == "windows" {
		fail = "exit 3"
	}
	if r := RunShell(t.TempDir(), fail); r.ExitCode != 3 || r.Err == nil {
		t.Fatalf("exit code: %+v", r)
	}
}
