package bridge

import (
	"context"

	"agentgo/internal/agentpack"
)

// ExportAgentPack bundles the selected artifacts into a shareable .agentpack file.
func (s *AppService) ExportAgentPack(title string, workflows, toolNames, innerapps []string, outPath string) map[string]any {
	if s.rt == nil || s.rt.agentPack == nil {
		return map[string]any{"success": false, "error": "agentpack unavailable"}
	}
	path, man, err := s.rt.agentPack.Export(context.Background(), agentpack.ExportRequest{
		Title:               title,
		Workflows:           workflows,
		Tools:               toolNames,
		InnerApps:           innerapps,
		IncludeDependencies: true,
		OutPath:             outPath,
	})
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "path": path, "items": man.Items}
}

// InspectAgentPack previews a pack's manifest without installing anything.
func (s *AppService) InspectAgentPack(path string) map[string]any {
	man, err := agentpack.Inspect(path)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "manifest": man}
}

// ImportAgentPack installs a shared pack. Packs containing executable tool code
// require confirm=true; otherwise a preview (need_confirm) is returned.
func (s *AppService) ImportAgentPack(path string, confirm, overwrite bool) map[string]any {
	if s.rt == nil || s.rt.agentPack == nil {
		return map[string]any{"success": false, "error": "agentpack unavailable"}
	}
	res, err := s.rt.agentPack.Import(context.Background(), path, confirm, overwrite)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "result": res}
}
