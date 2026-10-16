package core

import (
	"strings"
	"testing"

	"agentgo/internal/memory"
)

func TestRecallQAWithoutMemoryIsUnavailable(t *testing.T) {
	out := RecallQA(nil, "q", "", "", 0)
	if out["success"] != false || out["error"] != "memory unavailable" {
		t.Fatalf("got %v", out)
	}
}

func TestRecallQAScoringAndExplanation(t *testing.T) {
	rec := memory.Record{ID: "m1", Content: "User prefers Go and dark mode", Scope: "global", Importance: 5}
	row := recallQARow("go, dark theme", rec, 0)
	if row["rank"] != 1 || row["id"] != "m1" {
		t.Fatalf("row: %v", row)
	}
	// rank 0, importance clamped to 2 => 0.7 + 0.3 = 1.0
	if row["estimated_score"] != 1.0 {
		t.Fatalf("score: %v", row["estimated_score"])
	}
	why, _ := row["why"].(string)
	if !strings.Contains(why, "rank #1") || !strings.Contains(why, "2 query token hits") || !strings.Contains(why, "scope=global") {
		t.Fatalf("why: %q", why)
	}
	if recallApproxScore(memory.Record{}, 3) >= recallApproxScore(memory.Record{}, 0) {
		t.Fatal("a lower rank must score lower")
	}
}

func TestTokenOverlapIgnoresPunctuationShortTokensAndRepeats(t *testing.T) {
	if n := tokenOverlap("a, go! go go", "go is fun; a"); n != 1 {
		t.Fatalf("overlap=%d, want 1 (single-char tokens ignored, repeats counted once)", n)
	}
	if tokenOverlap("", "anything") != 0 {
		t.Fatal("empty query has no overlap")
	}
}
