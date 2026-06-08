package memory

import (
	"context"
	"testing"
)

func TestPipelineGraph(t *testing.T) {
	ctx := context.Background()
	// Create in-memory store
	store, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create sqlite store: %v", err)
	}
	defer store.DB().Close()

	pipeline := NewPipeline(store)

	mockCallerCalled := false
	mockCaller := func(ctx context.Context, system, user string) (string, error) {
		mockCallerCalled = true
		return "[EPISODE] mock response content from LLM", nil
	}

	// Compile the graph
	runnable, err := pipeline.BuildPipelineGraph(ctx, mockCaller)
	if err != nil {
		t.Fatalf("failed to compile pipeline graph: %v", err)
	}

	// Invoke the graph
	outErr, err := runnable.Invoke(ctx, PipelineInput{
		Scope:        "session_test_123",
		Conversation: "User: hello\nAssistant: hi there",
	})
	if err != nil {
		t.Fatalf("failed to run graph: %v", err)
	}
	if outErr != nil {
		t.Fatalf("graph returned error value: %v", outErr)
	}

	if !mockCallerCalled {
		t.Fatal("expected LLM caller to be called")
	}

	// Check if memory has records
	recs, err := pipeline.List(ctx, 10)
	if err != nil {
		t.Fatalf("list memories: %v", err)
	}

	if len(recs) == 0 {
		t.Fatal("expected memory records to be ingested by graph")
	}
}
