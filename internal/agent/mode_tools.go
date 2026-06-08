package agent

import (
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"agentgo/internal/tools"
)

// RegistryToolsForMode returns ADK-visible tools for the active session mode.
func RegistryToolsForMode(reg *tools.Registry, sm SessionMode) []einotool.BaseTool {
	if reg == nil {
		return nil
	}
	return reg.ToolsForMode(sm.StaticToolAllowlist(), sm.AllowsDynamicTool)
}

// RegistryDynamicToolsForMode returns toolsearch dynamic tools for the active mode.
func RegistryDynamicToolsForMode(reg *tools.Registry, sm SessionMode) []einotool.BaseTool {
	if reg == nil {
		return nil
	}
	return reg.GetDynamicToolsForMode(sm.AllowsDynamicTool)
}

func filterToolInfos(infos []*schema.ToolInfo, sm SessionMode) []*schema.ToolInfo {
	if len(infos) == 0 {
		return infos
	}
	allowStatic := sm.StaticToolAllowlist()
	out := make([]*schema.ToolInfo, 0, len(infos))
	for _, ti := range infos {
		if ti == nil {
			continue
		}
		name := ti.Name
		if tools.IsStaticTool(name) {
			if allowStatic != nil && !allowStatic[name] {
				continue
			}
		} else if !sm.AllowsDynamicTool(name) {
			continue
		}
		out = append(out, ti)
	}
	return out
}
