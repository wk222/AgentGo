package agent

import (
	"strings"

	"agentgo/internal/config"
	"agentgo/internal/governance"
	"agentgo/internal/tools"
)

// EnableSubagent reports whether invoke_subagent should be exposed for this mode.
func (m SessionMode) EnableSubagent() bool {
	p, c := m.normalized()
	if p == ModeRedTeam {
		return true
	}
	switch p {
	case ModeAdmin:
		return c != CanvasFocused
	case ModeAppMatrix:
		return c != CanvasFocused
	default:
		return c == CanvasDeep
	}
}

func (m SessionMode) normalized() (ModeProfile, ExecutionCanvas) {
	p, c := m.Profile, m.Canvas
	if p == ModeRedTeam && !config.ArmorBreak.On() {
		p = ModeAssistant
	}
	if p == "" {
		p = ModeAssistant
	}
	if c == "" {
		c = CanvasBalanced
	}
	return p, c
}

// StaticToolAllowlist returns allowed static tool names; nil means all registered static tools.
func (m SessionMode) StaticToolAllowlist() map[string]bool {
	p, c := m.normalized()
	switch p {
	case ModeRedTeam:
		return nil
	case ModeAdmin:
		return nil
	case ModeAppMatrix:
		allow := map[string]bool{
			"get_current_time":              true,
			"list_workspace_dir":            true,
			"register_workflow":             true,
			"register_app":                  true,
			"register_inner_app":            true,
			"scaffold_inner_app":            true,
			"update_inner_app_file":         true,
			"read_inner_app_file":           true,
			"list_inner_app_files":          true,
			"verify_inner_app":              true,
			"build_inner_app_iteratively":   true,
			"list_inner_apps":               true,
			"invoke_inner_app":              true,
			"invoke_app_capability":         true,
			"list_matrix_orchestrations":    true,
			"get_matrix_orchestration":      true,
			"save_matrix_orchestration":     true,
			"validate_matrix_orchestration": true,
			"run_matrix_orchestration":      true,
			"register_matrix_orchestration": true,
			"topology_validate":             true,
			"register_pipeline":             true,
			"list_pipelines":                true,
			"run_pipeline":                  true,
			"register_matrix_node":          true,
			"run_workflow":                  true,
			"activate_skill":                true,
			"ask_user":                      true,
			"create_tool":                   true,
			"create_tool_from_template":     true,
			"list_dynamic_tools":            true,
			"echo_message":                  true,
		}
		allowWorkspaceReadTools(allow)
		if c != CanvasFocused {
			allowWorkspaceActionTools(allow)
		}
		if m.EnableSubagent() {
			allow["invoke_subagent"] = true
			allow["run_swarm"] = true
		}
		allowPluginToolsForProfile(allow, p, c)
		return allow
	case ModeReverse:
		allow := map[string]bool{
			"get_current_time": true,
			"ask_user":         true,
			"echo_message":     true,
			"render_ui":        true,
		}
		allowWorkspaceReadTools(allow)
		if c != CanvasFocused {
			allowWorkspaceActionTools(allow)
			allow["activate_skill"] = true
		}
		if c == CanvasDeep {
			allow["run_workflow"] = true
			allow["list_dynamic_tools"] = true
		}
		if m.EnableSubagent() {
			allow["invoke_subagent"] = true
			allow["run_swarm"] = true
		}
		allowPluginToolsForProfile(allow, p, c)
		return allow
	default:
		allow := map[string]bool{
			"get_current_time": true,
			"ask_user":         true,
			"echo_message":     true,
			"render_ui":        true,
		}
		allowWorkspaceReadTools(allow)
		if c != CanvasFocused {
			allowWorkspaceActionTools(allow)
			allow["activate_skill"] = true
		}
		if c == CanvasDeep {
			allow["run_workflow"] = true
			allow["list_dynamic_tools"] = true
			allow["register_inner_app"] = true
			allow["scaffold_inner_app"] = true
			allow["update_inner_app_file"] = true
			allow["read_inner_app_file"] = true
			allow["list_inner_app_files"] = true
			allow["invoke_inner_app"] = true
			allow["invoke_app_capability"] = true
			allow["list_inner_apps"] = true
			allow["verify_inner_app"] = true
			allow["build_inner_app_iteratively"] = true
		}
		if c == CanvasBalanced {
			allow["scaffold_inner_app"] = true
			allow["update_inner_app_file"] = true
			allow["read_inner_app_file"] = true
			allow["list_inner_app_files"] = true
			allow["list_inner_apps"] = true
			allow["verify_inner_app"] = true
			allow["build_inner_app_iteratively"] = true
		}
		if m.EnableSubagent() {
			allow["invoke_subagent"] = true
			allow["run_swarm"] = true
		}
		allowPluginToolsForProfile(allow, p, c)
		return allow
	}
}

// allowPluginToolsForProfile admits tools plugins declared always-visible.
// Reverse-engineering tools (re_*) are scoped to ModeReverse (and ModeAdmin via nil allowlist)
// so ordinary chat profiles do not pay their schema token cost. File-mutating tools stay
// hidden in the read-only (focused) canvas.
func allowPluginToolsForProfile(allow map[string]bool, p ModeProfile, c ExecutionCanvas) {
	for name, mutating := range tools.PluginStaticTools() {
		if strings.HasPrefix(name, "re_") && p != ModeReverse && p != ModeAdmin {
			continue
		}
		if !mutating || c != CanvasFocused {
			allow[name] = true
		}
	}
}

func allowWorkspaceReadTools(allow map[string]bool) {
	for _, name := range []string{
		"list_workspace_dir",
		"read_workspace_file",
		"search_workspace",
		"get_workspace_changes",
		"parse_file",
	} {
		allow[name] = true
	}
}

func allowWorkspaceActionTools(allow map[string]bool) {
	for _, name := range []string{
		"replace_workspace_text",
		"create_workspace_file",
		"execute_bash",
		"run_batch_script",
		"run_elevated",
		"computer_use", // only registered when AGENTGO_COMPUTER_USE is on
	} {
		allow[name] = true
	}
}

// AllowsDynamicTool filters toolsearch / dynamic registry entries by profile × canvas.
func (m SessionMode) AllowsDynamicTool(name string) bool {
	originalName := strings.TrimSpace(name)
	name = strings.ToLower(originalName)
	if name == "" {
		return false
	}
	p, c := m.normalized()
	if p == ModeAdmin {
		return true
	}
	if c == CanvasFocused {
		return isReadOnlyDynamicTool(originalName)
	}
	if p == ModeAssistant && c == CanvasBalanced {
		if strings.Contains(name, "execute_bash") {
			return false
		}
	}
	return true
}

func isReadOnlyDynamicTool(name string) bool {
	if traits, ok := governance.ToolTraitsOf(name); ok && strings.HasPrefix(traits.Source, "mcp:") {
		return !traits.Mutating && traits.Risk == governance.RiskLow
	}
	if mut, ok := tools.PluginStaticTools()[name]; ok {
		return !mut
	}
	name = strings.ToLower(name)
	if strings.Contains(name, "bash") || strings.Contains(name, "execute") ||
		strings.Contains(name, "write") || strings.Contains(name, "python") {
		return false
	}
	return true
}

// MemoryDistillScope returns the scope key used by the background distill scheduler.
func (m SessionMode) MemoryDistillScope() string {
	p, _ := m.normalized()
	return "mode:" + string(p)
}
