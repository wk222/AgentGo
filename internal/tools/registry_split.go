package tools

import (
	"context"

	einotool "github.com/cloudwego/eino/components/tool"
)

// Static tool names are always visible to the model (no tool_search).
// IsStaticTool reports whether name is in the always-static ADK tool set.
func IsStaticTool(name string) bool {
	return staticToolNames[name]
}

var staticToolNames = map[string]bool{
	"get_current_time":            true,
	"echo_message":                true,
	"list_workspace_dir":          true,
	"create_tool":                 true,
	"create_tool_from_template":   true,
	"list_dynamic_tools":          true,
	"register_workflow":           true,
	"register_app":                true,
	"register_inner_app":          true,
	"scaffold_inner_app":          true,
	"update_inner_app_file":       true,
	"read_inner_app_file":         true,
	"list_inner_app_files":        true,
	"verify_inner_app":            true,
	"build_inner_app_iteratively": true,
	"list_inner_apps":             true,
	"invoke_inner_app":            true,
	"invoke_app_capability":       true,
	"ask_user":                    true,
	"activate_skill":              true,
	"invoke_subagent":             true,
	"run_swarm":                   true,
	"run_workflow":                true,
	"render_ui":                   true, // 指标卡/表单等 A2UI，必须直连不可只靠 tool_search
}

func toolName(ctx context.Context, t einotool.BaseTool) string {
	info, err := t.Info(ctx)
	if err != nil || info == nil {
		return ""
	}
	return info.Name
}

// GetStaticTools returns tools that stay in the agent ToolsConfig.
func (r *Registry) GetStaticTools() []einotool.BaseTool {
	return r.GetStaticToolsForMode(nil)
}

// GetStaticToolsForMode filters static tools by ModeProfile policy (allowlist nil = all static).
func (r *Registry) GetStaticToolsForMode(allow map[string]bool) []einotool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ctx := context.Background()
	var list []einotool.BaseTool
	for _, t := range r.tools {
		name := toolName(ctx, t)
		if name == "" {
			continue
		}
		if allow == nil {
			if staticToolNames[name] {
				list = append(list, t)
			}
			continue
		}
		if allow[name] {
			list = append(list, t)
		}
	}
	return list
}

// GetDynamicTools returns tools exposed via Eino toolsearch.
func (r *Registry) GetDynamicTools() []einotool.BaseTool {
	return r.GetDynamicToolsForMode(nil)
}

// GetDynamicToolsForMode filters dynamic tools; allowDynamic(name) nil = allow all non-static.
func (r *Registry) GetDynamicToolsForMode(allowDynamic func(string) bool) []einotool.BaseTool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ctx := context.Background()
	var list []einotool.BaseTool
	for _, t := range r.tools {
		name := toolName(ctx, t)
		if name == "" || staticToolNames[name] {
			continue
		}
		if allowDynamic != nil && !allowDynamic(name) {
			continue
		}
		list = append(list, t)
	}
	return list
}

// ToolsForADK picks static-only when dynamic tools exist (toolsearch middleware supplies the rest).
func (r *Registry) ToolsForADK() []einotool.BaseTool {
	return r.ToolsForMode(nil, nil)
}

// ToolsForMode applies ModeProfile static allowlist and dynamic filter.
func (r *Registry) ToolsForMode(staticAllow map[string]bool, allowDynamic func(string) bool) []einotool.BaseTool {
	static := r.GetStaticToolsForMode(staticAllow)
	dyn := r.GetDynamicToolsForMode(allowDynamic)
	if len(dyn) > 0 && len(static) > 0 {
		return static
	}
	if len(static) > 0 {
		return static
	}
	return r.GetAllTools()
}
