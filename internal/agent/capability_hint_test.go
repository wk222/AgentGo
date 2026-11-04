package agent

import (
	"context"
	"strings"
	"testing"

	"agentgo/internal/tools"
)

func TestDeferredCapabilityHintNamesOnlyWhatIsOffered(t *testing.T) {
	ctx := context.Background()
	reg := tools.NewRegistry()
	if got := deferredCapabilityHint(ctx, reg.GetDynamicTools()); got != "" {
		t.Fatalf("nothing registered, nothing to name: %q", got)
	}

	if err := tools.RegisterSessionMessageTool(reg); err != nil {
		t.Fatal(err)
	}
	got := deferredCapabilityHint(ctx, reg.GetDynamicTools())
	if !strings.Contains(got, "session_message") || strings.Contains(got, "external_agent") {
		t.Fatalf("only the registered one may be named: %q", got)
	}

	if err := tools.RegisterExternalAgentTool(reg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	got = deferredCapabilityHint(ctx, reg.GetDynamicTools())
	if !strings.Contains(got, "session_message") || !strings.Contains(got, "external_agent") || !strings.Contains(got, "tool_search") {
		t.Fatalf("both should be named: %q", got)
	}

	// A mode that hides dynamic tools passes none, so nothing is advertised.
	if got := deferredCapabilityHint(ctx, nil); got != "" {
		t.Fatalf("no dynamic tools, no hint: %q", got)
	}
}
