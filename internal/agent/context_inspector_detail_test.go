package agent

import (
	"strings"
	"testing"
)

func TestFinalizeWarnsNearWindowAndNamesLargestSegment(t *testing.T) {
	r := ContextBudgetReport{
		TotalTokens: 850, ModelContextWindow: 1000,
		Segments: []ContextSegment{{Source: "history", TokenCount: 600}, {Source: "memory", TokenCount: 250}},
		TopItems: []ContextItem{{Source: "history", Label: "tool result: big", TokenCount: 400}},
	}
	r.Finalize()
	if r.WindowPercent != 85 || len(r.Advice) != 2 || !strings.Contains(r.Advice[0], "history") || !strings.Contains(r.Advice[1], "tool result: big") {
		t.Fatalf("unexpected advice: %v (%.0f%%)", r.Advice, r.WindowPercent)
	}
	r.TotalTokens = 100
	r.Finalize()
	if len(r.Advice) != 0 {
		t.Fatal("advice must be cleared below the threshold", r.Advice)
	}
}

func TestAssistantEpisodeIsUnverifiedBoundedAndSkipsTrivialReplies(t *testing.T) {
	if _, ok := assistantEpisodeRecord("s", "好的", "x", nil); ok {
		t.Fatal("trivial reply must not become memory")
	}
	rec, ok := assistantEpisodeRecord("s", strings.Repeat("中", 9000), "x", map[string]interface{}{"mode_profile": "reverse"})
	if !ok || rec.Metadata["verification"] != "assistant_report_unverified" || rec.Metadata["truncated"] != true || rec.Metadata["mode_profile"] != "reverse" {
		t.Fatalf("bad record: ok=%v meta=%v", ok, rec.Metadata)
	}
	if n := len([]rune(rec.Content)); n > maxEpisodeRunes+40 {
		t.Fatal("episode not bounded", n)
	}
}
