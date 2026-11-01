package codetools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A capped list must not read as a total. A model asked "how many lines match"
// answered with the page size (100) because the header said "Found 100
// matches"; the true count was over 2000.
func TestCappedResultsSayTheyAreCapped(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < grepLimit*3; i++ {
		fmt.Fprintf(&b, "line %d ERROR code=9%02d\n", i, i%100)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < globLimit*2; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%03d.txt", i))
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &searcher{normalizeSearch(SearchOptions{WorkDir: dir, Rg: func() string { return "" }})}

	grepOut := s.grep(context.Background(), "", "", grepArgs{Pattern: `code=9\d{2}`, Path: "."})
	if grepOut.IsError {
		t.Fatalf("%s", grepOut.Content)
	}
	head, _, _ := strings.Cut(grepOut.Content, "\n")
	if !strings.Contains(head, "truncated") {
		t.Fatalf("grep header looks like a total: %q", head)
	}

	globOut := s.glob(context.Background(), "", "", globArgs{Pattern: "*.txt", Path: "."})
	if globOut.IsError {
		t.Fatalf("%s", globOut.Content)
	}
	globHead, _, _ := strings.Cut(globOut.Content, "\n")
	if !strings.Contains(globHead, "truncated") {
		t.Fatalf("glob header looks like a total: %q", globHead)
	}

	// Below the limit nothing is added: the plain answer stays plain.
	small := s.grep(context.Background(), "", "", grepArgs{Pattern: "code=900", Path: "."})
	if strings.Contains(small.Content, "truncated") {
		t.Fatalf("uncapped result claims truncation: %q", small.Content)
	}
}
