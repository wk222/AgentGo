package ideruntime_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentgo/internal/ideruntime"
)

func TestWorkspaceFS_URIConversion(t *testing.T) {
	sample := filepath.Join(os.TempDir(), "agentgo_test_workspace", "main.go")
	uri := ideruntime.PathToURI(sample)
	if !strings.HasPrefix(uri, "file://") {
		t.Fatalf("expected file:// scheme, got %q", uri)
	}

	back, err := ideruntime.URIToPath(uri)
	if err != nil {
		t.Fatalf("URIToPath error: %v", err)
	}

	if filepath.Clean(sample) != filepath.Clean(back) {
		t.Errorf("URI roundtrip mismatch: expected %q, got %q", sample, back)
	}
}

func TestWorkspaceFS_PathGuardSandbox(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agentgo-fs-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	fs, err := ideruntime.NewWorkspaceFS(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	// Legal file in workspace
	legalFile := filepath.Join(tempDir, "src", "index.ts")
	legalURI := ideruntime.PathToURI(legalFile)
	_, err = fs.WriteFile(legalURI, []byte("console.log('hi');"), "")
	if err != nil {
		t.Fatalf("legal write failed: %v", err)
	}

	// Illegal file outside workspace (e.g. parent dir traversal)
	illegalPath := filepath.Join(tempDir, "..", "escaped.txt")
	illegalURI := ideruntime.PathToURI(illegalPath)
	_, err = fs.WriteFile(illegalURI, []byte("forbidden"), "")
	if err == nil {
		t.Fatal("expected access denied error for escaped path, got nil")
	}
}

func TestWorkspaceFS_AtomicWriteAndVersionConflict(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agentgo-fs-version-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	fs, err := ideruntime.NewWorkspaceFS(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(tempDir, "config.json")
	targetURI := ideruntime.PathToURI(target)

	// 1. Initial write
	stat1, err := fs.WriteFile(targetURI, []byte(`{"version": 1}`), "")
	if err != nil {
		t.Fatalf("initial write failed: %v", err)
	}
	if stat1.Version == "" {
		t.Fatal("expected non-empty version tag")
	}

	// 2. Read back
	text, stat2, err := fs.ReadText(targetURI)
	if err != nil {
		t.Fatalf("read text failed: %v", err)
	}
	if text != `{"version": 1}` {
		t.Errorf("read text mismatch: %q", text)
	}

	// 3. Update with correct expectedVersion
	stat3, err := fs.WriteFile(targetURI, []byte(`{"version": 2}`), stat2.Version)
	if err != nil {
		t.Fatalf("write with correct version failed: %v", err)
	}

	// 4. Update with STALE expectedVersion (simulate Agent / UI race condition)
	_, err = fs.WriteFile(targetURI, []byte(`{"version": 3}`), stat1.Version)
	if err == nil {
		t.Fatal("expected version conflict error, got nil")
	}

	// Verify content remains version 2
	finalText, _, _ := fs.ReadText(targetURI)
	if finalText != `{"version": 2}` {
		t.Errorf("content overwritten despite conflict! got %q", finalText)
	}
	_ = stat3
}

func TestWorkspaceFS_DirectoryListingAndOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agentgo-fs-dir-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	fs, err := ideruntime.NewWorkspaceFS(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create subfolder and files
	subDirURI := ideruntime.PathToURI(filepath.Join(tempDir, "sub"))
	if err := fs.CreateDirectory(subDirURI); err != nil {
		t.Fatal(err)
	}

	f1URI := ideruntime.PathToURI(filepath.Join(tempDir, "a.txt"))
	f2URI := ideruntime.PathToURI(filepath.Join(tempDir, "b.txt"))
	_, _ = fs.WriteFile(f1URI, []byte("aaa"), "")
	_, _ = fs.WriteFile(f2URI, []byte("bbb"), "")

	// ReadDirectory on root
	rootURI := fs.RootURI()
	entries, err := fs.ReadDirectory(rootURI)
	if err != nil {
		t.Fatalf("ReadDirectory failed: %v", err)
	}

	if len(entries) != 3 {
		t.Fatalf("expected 3 entries (1 dir, 2 files), got %d", len(entries))
	}

	// Directories must sort first
	if !entries[0].IsDir || entries[0].Name != "sub" {
		t.Errorf("expected first entry to be directory 'sub', got %+v", entries[0])
	}

	// Copy and Rename
	copiedURI := ideruntime.PathToURI(filepath.Join(tempDir, "a_copy.txt"))
	if err := fs.Copy(f1URI, copiedURI); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}
	renamedURI := ideruntime.PathToURI(filepath.Join(tempDir, "a_renamed.txt"))
	if err := fs.Rename(copiedURI, renamedURI); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}

	// Delete
	if err := fs.Delete(f2URI); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
}

func TestWorkspaceFS_WatcherCoalesceBatch(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agentgo-fs-watch-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	fs, err := ideruntime.NewWorkspaceFS(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	batches := make([]ideruntime.FSChangeBatch, 0)

	fs.OnFilesChanged(func(batch ideruntime.FSChangeBatch) {
		mu.Lock()
		defer mu.Unlock()
		batches = append(batches, batch)
	})

	if err := fs.StartWatcher(); err != nil {
		t.Fatalf("StartWatcher failed: %v", err)
	}
	defer fs.StopWatcher()

	// Rapidly create multiple files to verify 100ms debounce
	for i := 0; i < 5; i++ {
		p := filepath.Join(tempDir, "burst_"+string(rune('1'+i))+".txt")
		_ = os.WriteFile(p, []byte("burst"), 0644)
	}

	// Wait for debounce flush
	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	count := len(batches)
	mu.Unlock()

	if count == 0 {
		t.Fatal("expected at least one coalesced batch event, got 0")
	}
}
