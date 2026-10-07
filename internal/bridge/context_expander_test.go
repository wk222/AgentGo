package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandUserMentions_FileAndKeywords(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "agentgo-mentions-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// Create sample test files
	sampleFile := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(sampleFile, []byte("Hello AgentGo Mentions!"), 0644); err != nil {
		t.Fatal(err)
	}

	rt := &Runtime{workspace: tempDir}
	svc := &AppService{rt: rt}

	ctx := context.Background()

	// 1. Test no mentions
	original := "Hello world without at"
	expanded := svc.expandUserMentions(ctx, original)
	if expanded != original {
		t.Fatalf("expected untouched text, got %q", expanded)
	}

	// 2. Test file mention
	fileQuery := "Can you explain @sample.txt for me?"
	expandedFile := svc.expandUserMentions(ctx, fileQuery)
	if !strings.Contains(expandedFile, "Context File: `sample.txt`") {
		t.Fatalf("expected sample.txt context block, got %s", expandedFile)
	}
	if !strings.Contains(expandedFile, "Hello AgentGo Mentions!") {
		t.Fatalf("expected file content in context, got %s", expandedFile)
	}

	// 3. Test @Codebase mention
	codebaseQuery := "Summarize @Codebase architecture"
	expandedCodebase := svc.expandUserMentions(ctx, codebaseQuery)
	if !strings.Contains(expandedCodebase, "Context: Codebase Overview") {
		t.Fatalf("expected codebase context block, got %s", expandedCodebase)
	}
}
