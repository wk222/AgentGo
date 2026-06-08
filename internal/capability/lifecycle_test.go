package capability

import (
	"database/sql"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestAssetLifecycleTransitions(t *testing.T) {
	// Legal transitions
	if err := AssertTransition(StatusDraft, StatusVerified); err != nil {
		t.Errorf("draft -> verified should be legal: %v", err)
	}
	if err := AssertTransition(StatusDraft, StatusPublished); err != nil {
		t.Errorf("draft -> published should be legal: %v", err)
	}
	if err := AssertTransition(StatusPublished, StatusDeprecated); err != nil {
		t.Errorf("published -> deprecated should be legal: %v", err)
	}
	if err := AssertTransition(StatusDeprecated, StatusRetired); err != nil {
		t.Errorf("deprecated -> retired should be legal: %v", err)
	}

	// Illegal transitions
	if err := AssertTransition(StatusPublished, StatusDraft); err == nil {
		t.Error("published -> draft should be illegal")
	}
	if err := AssertTransition(StatusRetired, StatusPublished); err == nil {
		t.Error("retired -> published should be illegal")
	}
}

func TestBusLifecycleOperations(t *testing.T) {
	dbFile := "test_capability_lifecycle.db"
	defer os.Remove(dbFile)

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}

	bus := NewBusWithStore(store)
	g := bus.Register("tool", "my_super_tool", "local", map[string]string{"foo": "bar"})

	// Verify ID
	expectedID := "tool:my_super_tool:local"
	if g.ID != expectedID {
		t.Errorf("expected ID %s, got %s", expectedID, g.ID)
	}

	// Default status should be published
	if g.Status != StatusPublished {
		t.Errorf("expected default StatusPublished, got %s", g.Status)
	}

	// Transition status
	err = bus.Transition(g.ID, StatusDeprecated)
	if err != nil {
		t.Fatalf("failed to transition status: %v", err)
	}

	// Reload from bus
	g2, ok := bus.Get(g.ID)
	if !ok || g2.Status != StatusDeprecated {
		t.Errorf("expected StatusDeprecated on reload, got %s (ok=%t)", g2.Status, ok)
	}

	// Verify
	bus.Verify(g.ID, "pass")
	g3, _ := bus.Get(g.ID)
	if g3.VerifyResult != "pass" || g3.LastVerifiedAt == 0 {
		t.Errorf("failed to verify asset: result=%s, verified_at=%d", g3.VerifyResult, g3.LastVerifiedAt)
	}

	// Deprecate and link replacement
	replacementID := "tool:new_tool:local"
	err = bus.Deprecate(g.ID, replacementID)
	if err != nil {
		t.Fatalf("failed to deprecate asset: %v", err)
	}
	g4, _ := bus.Get(g.ID)
	if g4.Status != StatusDeprecated || g4.SupersedesID != replacementID {
		t.Errorf("failed deprecation check: status=%s, supersedes=%s", g4.Status, g4.SupersedesID)
	}

	// List filtering
	depList := bus.ListByStatus(StatusDeprecated)
	if len(depList) != 1 || depList[0].ID != g.ID {
		t.Errorf("expected 1 deprecated asset in list, got %d", len(depList))
	}

	// Test Sync DTOs
	bus.SyncTools([]SyncToolDTO{
		{Name: "test_tool_dto", Description: "DTO desc", Scope: "global", RiskLevel: "medium"},
	})
	syncID := "tool:test_tool_dto:global"
	gSync, exists := bus.Get(syncID)
	if !exists || gSync.Metadata["description"] != "DTO desc" || gSync.RiskLevel != "medium" {
		t.Errorf("sync tool dto failed: exists=%t, desc=%s, risk=%s", exists, gSync.Metadata["description"], gSync.RiskLevel)
	}

	// Record Metrics and Query Stats
	bus.RecordMetric("tool", "my_super_tool", 250.5, 120)
	bus.RecordMetric("tool", "my_super_tool", 350.5, 130)

	// Sleep briefly to ensure database commits write
	time.Sleep(100 * time.Millisecond)

	stats, err := store.QueryStats("tool", "my_super_tool")
	if err != nil {
		t.Fatalf("failed to query stats: %v", err)
	}
	if stats.CallCount != 2 {
		t.Errorf("expected 2 calls in stats, got %d", stats.CallCount)
	}
	if stats.AvgLatency != 300.5 {
		t.Errorf("expected avg latency 300.5ms, got %f", stats.AvgLatency)
	}
}
