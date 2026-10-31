package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"agentgo/internal/config"
	"agentgo/internal/governance"
)

func TestArmorBreakHarnessHiddenUnlessEnvEnabled(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "0")
	if _, ok := HarnessByID(HarnessArmorBreak); ok {
		t.Fatal("armor_break must not register when AGENTGO_ARMOR_BREAK is off")
	}
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	h, ok := HarnessByID(HarnessArmorBreak)
	if !ok || h.Profile != ModeRedTeam {
		t.Fatalf("expected armor_break harness, got ok=%v %+v", ok, h)
	}
}

func TestRedTeamProfileDowngradesWhenFlagOff(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "0")
	sm := ParseSessionMode(string(ModeRedTeam), string(CanvasDeep))
	if sm.Profile != ModeAssistant {
		t.Fatalf("red_team without flag must downgrade, got %s", sm.Profile)
	}
}

func TestRedTeamOpensPolicyAndAutoApproves(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	r := &Runner{policy: governance.BuildPolicy("balanced", t.TempDir())}
	ctx := WithSessionMode(context.Background(), SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep})
	p := r.effectivePolicyForCtx(ctx)
	if p.Control.Mode != governance.ControlOpen || !p.SessionAutoApprove {
		t.Fatalf("red_team policy: mode=%s auto=%v", p.Control.Mode, p.SessionAutoApprove)
	}
	if p.RequiresApprovalFor("execute_bash", governance.ToolControlDecision{Allowed: true, Risk: governance.RiskCritical}) {
		t.Fatal("armor break must auto-approve bash")
	}
}

func TestRedTeamModeHintsMentionLocalTest(t *testing.T) {
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	t.Setenv("AGENTGO_ARMOR_BREAK_EXTRA", "EXTRA-LOCAL-SUFFIX")
	h := strings.TrimSpace(SessionMode{Profile: ModeRedTeam, Canvas: CanvasDeep}.ModeHints())
	if !strings.Contains(h, "armor-break") || !strings.Contains(h, "EXTRA-LOCAL-SUFFIX") {
		t.Fatalf("hints: %q", h)
	}
}

// Reverse enables the full armor-break bundle by default (flag unset): open
// policy + auto-approve, banner and memories, and the continuation gate.
// An explicit AGENTGO_ARMOR_BREAK=0 turns it back off. Other profiles stay off.
func TestReverseArmorBreakOnByDefault(t *testing.T) {
	if v, had := os.LookupEnv("AGENTGO_ARMOR_BREAK"); had {
		_ = os.Unsetenv("AGENTGO_ARMOR_BREAK")
		t.Cleanup(func() { _ = os.Setenv("AGENTGO_ARMOR_BREAK", v) })
	}
	rev := SessionMode{Profile: ModeReverse, Canvas: CanvasBalanced}
	if !rev.ArmorBreakActive() {
		t.Fatal("reverse must enable armor break by default")
	}
	ctx := WithSessionMode(context.Background(), rev)
	if !shouldArmorBreak(ctx, &RunResult{Content: "抱歉，我无法协助。"}) {
		t.Fatal("reverse continuation gate must trigger without the env flag")
	}
	if shouldArmorBreak(ctx, &RunResult{Content: "抱歉，我无法协助。", UsedTools: true}) {
		t.Fatal("tool-using runs must not be nudged")
	}
	r := &Runner{policy: governance.BuildPolicy("balanced", t.TempDir())}
	p := r.effectivePolicyForCtx(ctx)
	if p.Control.Mode != governance.ControlOpen || !p.SessionAutoApprove {
		t.Fatalf("reverse policy: mode=%s auto=%v", p.Control.Mode, p.SessionAutoApprove)
	}
	h := rev.ModeHints()
	for _, want := range []string{"[Armor-break session]", "auto-approve", "[Armor session memories", "[Reverse Harness Contract]"} {
		if !strings.Contains(h, want) {
			t.Fatalf("reverse hints missing %q", want)
		}
	}
	if strings.Contains(h, "AGENTGO_ARMOR_BREAK before shipping") {
		t.Fatal("reverse banner must not carry the red-team-only shipping note")
	}
	if !ArmorMemoriesActive(rev) {
		t.Fatal("armor memories must be active in reverse by default")
	}
	ast := WithSessionMode(context.Background(), SessionMode{Profile: ModeAssistant, Canvas: CanvasBalanced})
	if shouldArmorBreak(ast, &RunResult{Content: "抱歉，我无法协助。"}) {
		t.Fatal("assistant must never armor-break")
	}
	// Explicit opt-out beats the reverse default.
	t.Setenv("AGENTGO_ARMOR_BREAK", "0")
	if rev.ArmorBreakActive() || shouldArmorBreak(ctx, &RunResult{Content: "抱歉，我无法协助。"}) {
		t.Fatal("explicit AGENTGO_ARMOR_BREAK=0 must disable reverse armor break")
	}
	// And explicitly on is on (same as default).
	t.Setenv("AGENTGO_ARMOR_BREAK", "1")
	if !rev.ArmorBreakActive() {
		t.Fatal("explicit AGENTGO_ARMOR_BREAK=1 must enable reverse armor break")
	}
}

func TestArmorBreakEnvRegisteredInConfig(t *testing.T) {
	_ = os.Getenv("AGENTGO_ARMOR_BREAK")
	if config.ArmorBreak.Name() != "AGENTGO_ARMOR_BREAK" {
		t.Fatal(config.ArmorBreak.Name())
	}
}
