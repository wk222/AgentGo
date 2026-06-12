package agent

import (
	"strings"
	"testing"
)

func TestRepairToolArgsJSON(t *testing.T) {
	cases := []struct {
		in, wantSub string
	}{
		{`{"input":"hi"}`, `"input"`},
		{`{"arguments":{"app_name":"demo"}}`, `"app_name"`},
		{"```json\n{\"x\":1}\n```", `"x"`},
		{`[{"workflow_id":"wf1"}]`, `"workflow_id"`},
	}
	for _, c := range cases {
		got := RepairToolArgsJSON(c.in)
		if !strings.Contains(got, c.wantSub) {
			t.Fatalf("RepairToolArgsJSON(%q) = %q, want contains %q", c.in, got, c.wantSub)
		}
	}
}
