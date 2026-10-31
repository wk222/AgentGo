package bridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeAt(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestFindDroppedFileNeedsNameSizeAndTime(t *testing.T) {
	desktop, docs := t.TempDir(), t.TempDir()
	mtime := time.Date(2026, 10, 27, 9, 30, 0, 0, time.Local)
	want := filepath.Join(docs, "WeChat Files", "wxid_x", "FileStorage", "File", "2026-10", "全光提速实时通报2610(1)(1).xlsx")
	writeAt(t, want, "0123456789", mtime)
	// Decoys: same name with other content / other time, and a different name.
	writeAt(t, filepath.Join(desktop, "全光提速实时通报2610(1)(1).xlsx"), "01234", mtime)
	writeAt(t, filepath.Join(desktop, "old", "全光提速实时通报2610(1)(1).xlsx"), "0123456789", mtime.Add(-48*time.Hour))
	writeAt(t, filepath.Join(desktop, "other.xlsx"), "0123456789", mtime)
	roots := []searchRoot{{desktop, 4}, {docs, 8}}
	ctx := context.Background()
	name := "全光提速实时通报2610(1)(1).xlsx"

	if got := findDroppedFile(ctx, roots, name, 10, mtime.UnixMilli()); got != want {
		t.Fatalf("found %q, want the one that matches name, size and time: %q", got, want)
	}
	if got := findDroppedFile(ctx, roots, name, 10, mtime.UnixMilli()+1500); got != want {
		t.Errorf("a time off by less than the FAT slack must still match: %q", got)
	}
	if got := findDroppedFile(ctx, roots, name, 10, mtime.UnixMilli()+60000); got != "" {
		t.Errorf("a different time is a different file: %q", got)
	}
	if got := findDroppedFile(ctx, roots, "NAME-NOT-HERE.xlsx", 10, mtime.UnixMilli()); got != "" {
		t.Errorf("nothing named like that: %q", got)
	}
	// No reported time: name and size decide (the desktop decoy has another size, the old one matches).
	if got := findDroppedFile(ctx, roots, name, 10, 0); got == "" {
		t.Error("without a time, name and size must still find a file")
	}
	// Name matching ignores case, as Windows does.
	if got := findDroppedFile(ctx, roots, "全光提速实时通报2610(1)(1).XLSX", 10, mtime.UnixMilli()); got != want {
		t.Errorf("case-insensitive name: %q", got)
	}
}

func TestFindDroppedFileHonoursDepthAndSkippedFolders(t *testing.T) {
	root := t.TempDir()
	mtime := time.Now().Add(-time.Hour).Truncate(time.Second)
	deep := filepath.Join(root, "a", "b", "c", "d", "x.xlsx")
	writeAt(t, deep, "abc", mtime)
	writeAt(t, filepath.Join(root, "node_modules", "pkg", "y.xlsx"), "abc", mtime)
	ctx := context.Background()

	if got := findDroppedFile(ctx, []searchRoot{{root, 2}}, "x.xlsx", 3, mtime.UnixMilli()); got != "" {
		t.Errorf("deeper than the allowed depth must not be searched: %q", got)
	}
	if got := findDroppedFile(ctx, []searchRoot{{root, 6}}, "x.xlsx", 3, mtime.UnixMilli()); got != deep {
		t.Errorf("within depth: %q", got)
	}
	if got := findDroppedFile(ctx, []searchRoot{{root, 6}}, "y.xlsx", 3, mtime.UnixMilli()); got != "" {
		t.Errorf("node_modules is never searched: %q", got)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := findDroppedFile(cancelled, []searchRoot{{root, 6}}, "x.xlsx", 3, mtime.UnixMilli()); got != "" {
		t.Errorf("a cancelled search returns nothing: %q", got)
	}
}
