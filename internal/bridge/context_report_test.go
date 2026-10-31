package bridge

import (
	"encoding/json"
	"strings"
	"testing"

	"agentgo/internal/agent"
)

func TestGetContextReportExposesDetailAndHandlesUnknownSession(t *testing.T) {
	s := &AppService{}
	if got := s.GetContextReport("never-ran"); got["available"] != false || got["success"] != true {
		t.Fatalf("unknown session must report unavailable: %v", got)
	}
	if got := s.GetContextReport("  "); got["available"] != false {
		t.Fatalf("blank session must report unavailable: %v", got)
	}
	r := agent.ContextBudgetReport{
		TotalTokens: 900, ModelContextWindow: 1000,
		Segments: []agent.ContextSegment{{Source: "history", TokenCount: 900, Percentage: 100}},
		TopItems: []agent.ContextItem{{Source: "history", Label: "tool result: big", TokenCount: 500}},
		Turns:    []agent.ContextTurn{{Index: 1, Title: "q", Messages: 3, TokenCount: 900}},
	}
	r.Finalize()
	agent.SaveContextBudgetReport("ctx-report-s1", r)
	got := s.GetContextReport("agentic:ctx-report-s1")
	if got["available"] != true {
		t.Fatalf("saved report not returned: %v", got)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"window_percent":90`, `"top_items"`, `"turns"`, `"advice"`, `tool result: big`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in %s", want, data)
		}
	}
}
