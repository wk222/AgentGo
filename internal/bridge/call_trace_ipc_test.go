package bridge

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/tool"

	"agentgo/internal/agent"
)

func TestCallTraceIPCSwitchAndRead(t *testing.T) {
	s := &AppService{}
	t.Cleanup(func() { s.SetCallTrace(false); s.ClearCallTrace("") })
	s.ClearCallTrace("")

	if got := s.SetCallTrace(true); got["enabled"] != true {
		t.Fatalf("switch on: %v", got)
	}
	ctx := agent.BindRunContext(context.Background(), "agentic:ipc1")
	ep, err := agent.NewSafeToolMiddleware().WrapInvokableToolCall(ctx,
		func(context.Context, string, ...tool.Option) (string, error) { return "ok", nil },
		&adk.ToolContext{Name: "code_view", CallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ep(ctx, `{}`); err != nil {
		t.Fatal(err)
	}

	got := s.GetCallTrace("", "", "")
	if got["enabled"] != true || got["events"] != nil {
		t.Fatalf("without a session only the switch and the session list come back: %v", got)
	}
	got = s.GetCallTrace("ipc1", "", "call-1")
	events, _ := got["events"].([]agent.CallTraceEvent)
	if len(events) != 2 || events[0].Stage != agent.TraceStageModelArgs || events[1].Stage != agent.TraceStageResult || events[1].CallID != "call-1" {
		t.Fatalf("events = %v", got["events"])
	}
	if got := s.GetCallTrace("nobody", "", ""); got["events"] == nil {
		t.Fatal("unknown session must give an empty list, not null")
	}
	s.ClearCallTrace("ipc1")
	if got := s.GetCallTrace("ipc1", "", ""); len(got["events"].([]agent.CallTraceEvent)) != 0 {
		t.Fatalf("not cleared: %v", got["events"])
	}
}
