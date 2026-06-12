package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/agent"
	"agentgo/internal/governance"
	"agentgo/internal/memory"
	"agentgo/internal/workspace"
)

// A CLI driver/test block to run local smoke tests for AgentGo's new core architecture.
func main() {
	fmt.Println("--- Starting AgentGo Core Architecture Local Integration Test ---")

	ctx := context.Background()
	tempDir, err := os.MkdirTemp("", "agentgo-test-*")
	if err != nil {
		log.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create dummy workspace files
	os.WriteFile(filepath.Join(tempDir, "SOUL.md"), []byte("I am AgentGo, a highly capable assistant."), 0644)
	os.WriteFile(filepath.Join(tempDir, "TEAM.md"), []byte("I work with the platform engineering team."), 0644)
	os.MkdirAll(filepath.Join(tempDir, ".cursor", "rules"), 0755)
	os.WriteFile(filepath.Join(tempDir, ".cursor", "rules", "rule1.mdc"), []byte("Always use Go for backend."), 0644)

	dbPath := filepath.Join(tempDir, "agentgo.db")
	fmt.Printf("Using temporary SQLite database: %s\n", dbPath)

	// 1. Initialize Memory Engine
	store, err := memory.NewSQLiteStore(dbPath)
	if err != nil {
		log.Fatalf("failed to init SQLiteStore: %v", err)
	}

	// 2. Initialize Approval Queue
	queue, err := governance.NewApprovalQueue(dbPath)
	if err != nil {
		log.Fatalf("failed to init ApprovalQueue: %v", err)
	}

	// 3. Setup Middlewares
	memMiddleware := agent.NewMemoryMiddleware(store)
	_ = memMiddleware // Keep compiler happy for this test
	govMiddleware := governance.NewGovernanceMiddleware(queue, governance.Policy{
		ToolRiskLevels: map[string]governance.RiskLevel{
			"execute_bash": governance.RiskCritical,
		},
	})
	wsMiddleware := workspace.NewContextMiddleware(tempDir)

	fmt.Println("✔ Created Memory, Governance, and Workspace Eino ADK middlewares.")

	// Test Workspace Middleware Logic
	fmt.Println("\n-- Running Workspace Context Injection Test --")
	dummyState := &adk.ChatModelAgentState{
		Messages: []*schema.Message{
			{Role: schema.System, Content: "You are a helpful assistant."},
		},
	}
	_, injectedState, err := wsMiddleware.BeforeModelRewriteState(ctx, dummyState, nil)
	if err != nil {
		log.Fatalf("failed workspace context injection: %v", err)
	}
	fmt.Println("✔ System prompt after workspace injection:")
	fmt.Println(injectedState.Messages[0].Content)

	// 4. Test Governance Middleware Interception Flow
	fmt.Println("\n-- Running High-Risk Tool Interception Test --")
	fakeEndpoint := func(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
		return "Mock Bash Success: rm -rf /", nil
	}

	wrapped, err := govMiddleware.WrapInvokableToolCall(ctx, func(ctx context.Context, args string, opts ...tool.Option) (string, error) {
		return fakeEndpoint(ctx, args, opts...)
	}, &adk.ToolContext{
		Name: "execute_bash",
	})
	if err != nil {
		log.Fatalf("failed to wrap tool call: %v", err)
	}

	// First execution should trigger interception because it's not approved yet
	resp, err := wrapped(ctx, `{"command": "rm -rf /"}`)
	if err != nil {
		log.Fatalf("wrapped call failed: %v", err)
	}
	fmt.Println("✔ Execution response received (waiting for approval):")
	fmt.Println(resp)

	// Check if the pending request is in the DB
	pending, err := queue.ListPending(ctx, nil)
	if err != nil {
		log.Fatalf("failed to list pending: %v", err)
	}
	if len(pending) > 0 {
		appr := pending[0]
		err = queue.Resolve(ctx, appr.ID, true, "Approved for testing safety", "admin", &governance.ResumePayload{Approved: true})
		if err != nil {
			log.Fatalf("failed to resolve: %v", err)
		}

		fmt.Println("\n-- Executing Wrapped Tool Second Time (After Approval) --")
		approvedResp, err := wrapped(ctx, `{"command": "rm -rf /"}`)
		if err != nil {
			log.Fatalf("wrapped call failed on approved: %v", err)
		}
		fmt.Printf("✔ Real execution result: %s\n", approvedResp)
	}

	fmt.Println("\n--- Integration Test Completed Successfully! ---")
}
