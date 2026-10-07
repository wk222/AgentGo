package ideruntime_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agentgo/internal/ideruntime"
)

func TestSearchService_TextAndFiles(t *testing.T) {
	sup, err := ideruntime.NewProcessSupervisor()
	if err != nil {
		t.Fatalf("NewProcessSupervisor: %v", err)
	}
	defer sup.Close()

	tempDir, err := os.MkdirTemp("", "agentgo-search-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	fs, err := ideruntime.NewWorkspaceFS(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	// Create test files
	file1 := filepath.Join(tempDir, "hello.txt")
	file2 := filepath.Join(tempDir, "world.go")
	_ = os.WriteFile(file1, []byte("Hello AgentGo IDE!\nLine 2"), 0644)
	_ = os.WriteFile(file2, []byte("package main\n// Hello AgentGo from Go"), 0644)

	searchSvc := ideruntime.NewSearchService(sup, fs)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Search files
	files, err := searchSvc.SearchFiles(ctx, "world", 10)
	if err != nil {
		t.Fatalf("SearchFiles failed: %v", err)
	}
	if len(files) != 1 {
		t.Errorf("expected 1 file matching 'world', got %d", len(files))
	}

	// 2. Search text
	results, err := searchSvc.SearchText(ctx, ideruntime.SearchQuery{
		Pattern:       "AgentGo",
		CaseSensitive: true,
	})
	if err != nil {
		t.Fatalf("SearchText failed: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 files with 'AgentGo', got %d", len(results))
	}
}

func TestGitService_Status(t *testing.T) {
	sup, err := ideruntime.NewProcessSupervisor()
	if err != nil {
		t.Fatalf("NewProcessSupervisor: %v", err)
	}
	defer sup.Close()

	// Use actual agentgo repo root
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}

	fs, err := ideruntime.NewWorkspaceFS(repoRoot)
	if err != nil {
		t.Fatal(err)
	}

	gitSvc := ideruntime.NewGitService(sup, fs)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	status, err := gitSvc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}

	if status.Branch == "" {
		t.Errorf("expected branch to be detected, got empty")
	}

	t.Logf("Detected git branch: %s, staged: %d, unstaged: %d, untracked: %d",
		status.Branch, len(status.Staged), len(status.Unstaged), len(status.Untracked))
}
