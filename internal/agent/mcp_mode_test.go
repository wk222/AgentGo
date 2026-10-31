package agent

import (
	"agentgo/internal/governance"
	"testing"
)

func TestFocusedMCPExposureUsesDeclaredRiskNotToolName(t *testing.T) {
	mode := SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused}
	remove := governance.RegisterToolTraits("Mcp_Ping", governance.ToolTraits{Source: "mcp:Mcp", Risk: governance.RiskHigh, Mutating: true})
	if mode.AllowsDynamicTool("Mcp_Ping") {
		t.Fatal("innocent name bypassed focused MCP policy")
	}
	remove()
	defer governance.RegisterToolTraits("Mcp_Ping", governance.ToolTraits{Source: "mcp:Mcp", Risk: governance.RiskLow})()
	if !mode.AllowsDynamicTool("Mcp_Ping") {
		t.Fatal("declared read-only MCP tool hidden")
	}
}
