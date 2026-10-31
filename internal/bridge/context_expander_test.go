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

// What the model receives for "/review ..." is the command's instructions plus
// the context of any @mention in its argument; an ordinary message passes
// through exactly as before.
func TestExpandUserInput_ReviewCommandAndMentions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample.txt"), []byte("Hello AgentGo Mentions!"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &AppService{rt: &Runtime{workspace: dir}}
	ctx := context.Background()

	got := svc.expandUserInput(ctx, "/review 重点看 @sample.txt")
	for _, want := range []string{
		"/review 重点看 @sample.txt", // what the user typed stays visible
		"get_workspace_changes",   // the review instructions
		"审查重点（优先看这些，但不排斥发现其他问题）：重点看 @sample.txt",
		"Hello AgentGo Mentions!", // the mention is still expanded
	} {
		if !strings.Contains(got, want) {
			t.Errorf("model input lacks %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "<command-instructions") > strings.Index(got, "Auto-Injected Context") {
		t.Errorf("the mention context must come after the command instructions:\n%s", got)
	}

	if plain := "解释一下这个函数"; svc.expandUserInput(ctx, plain) != plain {
		t.Errorf("an ordinary message must not change")
	}
}
