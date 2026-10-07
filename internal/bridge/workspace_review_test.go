package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceReviewRejectRestoresPreRunState(t *testing.T) {
	root := t.TempDir()
	mustWriteReviewTestFile(t, root, "dirty.txt", "user change before AI")
	mustWriteReviewTestFile(t, root, "deleted.txt", "keep me")

	store := newWorkspaceReviewStore()
	if resumed, pending, _, err := store.begin("session", root); err != nil || resumed || pending != 0 {
		t.Fatalf("begin = resumed %v pending %d err %v", resumed, pending, err)
	}
	mustWriteReviewTestFile(t, root, "dirty.txt", "AI rewrite")
	mustWriteReviewTestFile(t, root, "added.txt", "AI addition")
	if err := os.Remove(filepath.Join(root, "deleted.txt")); err != nil {
		t.Fatal(err)
	}

	_, changes, err := store.list("session", root)
	if err != nil || len(changes) != 3 {
		t.Fatalf("changes = %v err %v", changes, err)
	}
	if _, err := store.reject("session", root, "dirty.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.reject("session", root, "added.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.reject("session", root, "deleted.txt"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(root, "dirty.txt"))
	if err != nil || string(got) != "user change before AI" {
		t.Fatalf("dirty.txt = %q err %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(root, "added.txt")); !os.IsNotExist(err) {
		t.Fatalf("added.txt should be removed, err %v", err)
	}
	got, err = os.ReadFile(filepath.Join(root, "deleted.txt"))
	if err != nil || string(got) != "keep me" {
		t.Fatalf("deleted.txt = %q err %v", got, err)
	}
}

func TestWorkspaceReviewAcceptKeepsCurrentFile(t *testing.T) {
	root := t.TempDir()
	mustWriteReviewTestFile(t, root, "main.go", "before")
	store := newWorkspaceReviewStore()
	if _, _, _, err := store.begin("session", root); err != nil {
		t.Fatal(err)
	}
	mustWriteReviewTestFile(t, root, "main.go", "after")
	remaining, err := store.accept("session", root, "main.go")
	if err != nil || remaining != 0 {
		t.Fatalf("accept remaining %d err %v", remaining, err)
	}
	got, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || string(got) != "after" {
		t.Fatalf("main.go = %q err %v", got, err)
	}
	review, changes, err := store.list("session", root)
	if err != nil || review != nil || len(changes) != 0 {
		t.Fatalf("review = %#v changes %v err %v", review, changes, err)
	}
}

func TestWorkspaceReviewBeginDoesNotOverwritePendingBaseline(t *testing.T) {
	root := t.TempDir()
	mustWriteReviewTestFile(t, root, "main.go", "before")
	store := newWorkspaceReviewStore()
	if _, _, _, err := store.begin("session", root); err != nil {
		t.Fatal(err)
	}
	mustWriteReviewTestFile(t, root, "main.go", "first AI change")
	resumed, pending, _, err := store.begin("session", root)
	if err != nil || !resumed || pending != 1 {
		t.Fatalf("second begin = resumed %v pending %d err %v", resumed, pending, err)
	}
	if _, err := store.reject("session", root, "main.go"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "main.go"))
	if string(got) != "before" {
		t.Fatalf("baseline was overwritten: %q", got)
	}
}

func mustWriteReviewTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
