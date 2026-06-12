package agent

import (
	"testing"

	"agentgo/internal/governance"
	"agentgo/internal/tools"
)

func TestEffectivePolicyMarksDynamicCompiledToolsHighRisk(t *testing.T) {
	reg := tools.NewRegistry()
	reg.SetDynamicSandbox(t.TempDir())
	if err := reg.RegisterDynamicTool(tools.DynamicToolDef{
		Name:        "generated_reporter",
		Description: "Generated test tool",
		Code:        `print("ok")`,
	}); err != nil {
		t.Fatal(err)
	}

	base := governance.DefaultPolicy()
	r := &Runner{policy: base, toolReg: reg}
	got := r.effectivePolicy()
	if got.ToolRiskLevels["generated_reporter"] != governance.RiskHigh {
		t.Fatalf("dynamic tool risk=%q, want %q", got.ToolRiskLevels["generated_reporter"], governance.RiskHigh)
	}
	if base.ToolRiskLevels["generated_reporter"] != "" {
		t.Fatal("effectivePolicy should not mutate the base policy")
	}
}
