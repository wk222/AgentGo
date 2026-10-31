package spill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionDirectoryCannotEscapeSpillStore(t *testing.T) {
	store, err := NewFileSpillStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"..", "../other", `..\other`, `.`, `C:\other`} {
		if _, err := store.Save(context.Background(), sid, "c", "parse_file", []byte("body")); err == nil {
			t.Fatalf("unsafe session %q saved", sid)
		}
		if err := store.CleanupSession(context.Background(), sid); err == nil {
			t.Fatalf("unsafe session %q cleanup", sid)
		}
	}
}

func TestSaveFailsWithoutDurableIndex(t *testing.T) {
	root := t.TempDir()
	store, err := NewFileSpillStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(root, "index.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if record, err := store.Save(context.Background(), "s", "c", "parse_file", []byte("source")); err == nil || record != nil {
		t.Fatal("issued a handle without durable index")
	}
	if len(store.records) != 0 {
		t.Fatal("failed save remained registered")
	}
}
