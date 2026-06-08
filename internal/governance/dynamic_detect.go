package governance

// isLikelyDynamicTool detects runtime-registered tools (runner marks them in ToolRiskLevels).
func isLikelyDynamicTool(toolName string, p Policy) bool {
	if toolName == "execute_dynamic_tool" {
		return true
	}
	if _, inBase := defaultRiskTable()[toolName]; inBase {
		return false
	}
	_, listed := p.ToolRiskLevels[toolName]
	return listed
}
