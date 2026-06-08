package governance

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

func TestGovernanceAuditing(t *testing.T) {
	dbFile := "test_governance_audit.db"
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	queue, err := NewApprovalQueueWithDB(db)
	if err != nil {
		t.Fatalf("failed to create approval queue: %v", err)
	}

	ctx := WithSessionID(context.Background(), "test_session_123")

	// 1. Manually Record Audit
	err = queue.RecordAudit(ctx, AuditEntry{
		Channel:   "agent",
		SessionID: "test_session_123",
		Action:    "test_action",
		ToolName:  "my_tool",
		Arguments: `{"arg": 1}`,
		Result:    "success",
		RiskLevel: "LOW",
		Explanation: "Manual test log",
	})
	if err != nil {
		t.Fatalf("failed to record audit: %v", err)
	}

	// 2. Query Audit
	logs, err := queue.QueryAudit(ctx, "test_session_123")
	if err != nil {
		t.Fatalf("failed to query audit: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 log entry, got %d", len(logs))
	} else {
		entry := logs[0]
		if entry.ToolName != "my_tool" || entry.Result != "success" || entry.Explanation != "Manual test log" {
			t.Errorf("unexpected log entry content: %+v", entry)
		}
	}

	// 3. Test InvokeWithPolicy Auditing
	policy := Policy{
		ToolRiskLevels: map[string]RiskLevel{
			"blocked_tool": RiskCritical,
		},
		BlockedTools: map[string]bool{
			"blocked_tool": true,
		},
	}
	mw := NewGovernanceMiddleware(queue, policy)

	_, invokeErr := mw.InvokeWithPolicy(ctx, "blocked_tool", "{}", func(ctx context.Context, args string) (string, error) {
		return "ok", nil
	})
	if invokeErr == nil {
		t.Error("expected blocked_tool invocation to fail")
	}

	// Should have generated a policy_deny audit log
	logsAfter, err := queue.QueryAudit(ctx, "test_session_123")
	if err != nil {
		t.Fatalf("failed to query audit after: %v", err)
	}
	foundDeny := false
	for _, entry := range logsAfter {
		if entry.Action == "policy_deny" && entry.ToolName == "blocked_tool" {
			foundDeny = true
		}
	}
	if !foundDeny {
		t.Error("expected policy_deny audit log to be generated")
	}
}
