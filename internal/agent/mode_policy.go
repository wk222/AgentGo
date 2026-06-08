package agent

import "strings"

// EnableSubagent reports whether invoke_subagent should be exposed for this mode.
func (m SessionMode) EnableSubagent() bool {
	p, c := m.normalized()
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
	case ModeAdmin:
		return nil
	case ModeAppMatrix:
		allow := map[string]bool{
			"get_current_time":   true,
			"list_workspace_dir": true,
			"register_workflow":  true,
			"register_app":        true,
			"register_inner_app":     true,
			"scaffold_inner_app":     true,
			"update_inner_app_file":  true,
			"read_inner_app_file":    true,
			"list_inner_app_files":   true,
			"verify_inner_app":       true,
			"build_inner_app_iteratively": true,
			"list_inner_apps":        true,
			"invoke_inner_app":       true,
			"invoke_app_capability":  true,
			"list_matrix_orchestrations": true,
			"get_matrix_orchestration":   true,
			"save_matrix_orchestration":  true,
			"validate_matrix_orchestration": true,
			"run_matrix_orchestration":     true,
			"register_matrix_orchestration": true,
			"topology_validate":             true,
			"register_pipeline":             true,
			"list_pipelines":                true,
			"run_pipeline":                  true,
			"register_matrix_node":          true,
			"run_workflow":        true,
			"activate_skill":     true,
			"ask_user":           true,
			"create_tool":                 true,
			"create_tool_from_template":   true,
			"list_dynamic_tools": true,
			"echo_message":       true,
		}
		if m.EnableSubagent() {
			allow["invoke_subagent"] = true
			allow["run_swarm"] = true
		}
		return allow
	default:
		allow := map[string]bool{
			"get_current_time": true,
			"ask_user":         true,
			"echo_message":     true,
			"render_ui":        true,
		}
		if c != CanvasFocused {
			allow["list_workspace_dir"] = true
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
		return allow
	}
}

// AllowsDynamicTool filters toolsearch / dynamic registry entries by profile × canvas.
func (m SessionMode) AllowsDynamicTool(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	p, c := m.normalized()
	if p == ModeAdmin {
		return true
	}
	if c == CanvasFocused {
		return isReadOnlyDynamicTool(name)
	}
	if p == ModeAssistant && c == CanvasBalanced {
		if strings.Contains(name, "execute_bash") {
			return false
		}
	}
	return true
}

func isReadOnlyDynamicTool(name string) bool {
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
