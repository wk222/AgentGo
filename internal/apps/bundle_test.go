package apps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBundleDirRejectsTraversalBundlePath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, AppEntryFile), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	rel, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	if dir, ok := ResolveBundleDir(root, "", InnerApp{Name: "demo", BundlePath: rel}); ok {
		t.Fatalf("ResolveBundleDir accepted escaped bundle path: %s", dir)
	}
}

func TestResolveBundleDirRejectsAbsoluteOutsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, AppEntryFile), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dir, ok := ResolveBundleDir(root, "", InnerApp{Name: "demo", BundlePath: outside}); ok {
		t.Fatalf("ResolveBundleDir accepted absolute outside root: %s", dir)
	}
}

func TestReadBundleFileRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, AppEntryFile), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadBundleFile(root, "../secret.txt"); err == nil {
		t.Fatal("expected traversal read to be rejected")
	}
}
