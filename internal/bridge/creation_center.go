package bridge

import (
	"agentgo/internal/agentpack"
	"context"
	"encoding/json"
)

func (s *AppService) GetCreationCenter(ctx context.Context) map[string]any {
	if s.rt == nil || s.rt.agentPack == nil {
		return map[string]any{"success": false, "error": "extension engine unavailable"}
	}
	data, err := s.rt.agentPack.Catalog(ctx)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	versions, err := s.rt.agentPack.Versions()
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	skillList := []map[string]string{}
	if s.rt.skillLoader != nil {
		for _, skill := range s.rt.skillLoader.Reload() {
			skillList = append(skillList, map[string]string{"name": skill.Name, "description": skill.Description})
		}
	}
	data["skills"] = skillList
	data["versions"] = versions
	data["success"] = true
	available := []string{}
	for _, name := range []string{"create_tool", "scaffold_inner_app", "update_inner_app_file", "verify_inner_app", "save_workflow_definition", "define_workflow_node", "evolve_skill", "manage_extension_versions"} {
		if _, ok := s.rt.toolReg.Get(name); ok {
			available = append(available, name)
		}
	}
	data["available_tools"] = available
	return data
}
func (s *AppService) ExtensionVersions(ctx context.Context, action, payload string) map[string]any {
	if s.rt == nil || s.rt.agentPack == nil {
		return map[string]any{"success": false, "error": "extension engine unavailable"}
	}
	var req agentpack.VersionRequest
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &req); err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
	}
	req.Action = action
	return s.rt.agentPack.VersionCommand(ctx, req)
}
