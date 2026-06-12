package bridge

import (
	"context"
	"strings"

	"agentgo/internal/apps"
)

// ScaffoldInnerApp creates UI bundle files under data/apps (PyBot create_app parity).
func (s *AppService) ScaffoldInnerApp(name, displayName, description, mode, workflowID, systemPrompt, exportsCSV string, overwrite bool) map[string]any {
	d := s.rt.desktopApps()
	if d == nil {
		return map[string]any{"success": false, "error": "apps not configured"}
	}
	res := d.Scaffold(context.Background(), apps.ScaffoldOptions{
		Name: name, DisplayName: displayName, Description: description,
		Mode: mode, WorkflowID: workflowID, SystemPrompt: systemPrompt,
		Exports: apps.ParseExportsCSV(exportsCSV), Overwrite: overwrite,
	})
	return apps.ToJSONMap(res)
}

// UpdateInnerAppBundleFile writes a file in the app bundle with validation.
func (s *AppService) UpdateInnerAppBundleFile(appName, filePath, content string) map[string]any {
	d := s.rt.desktopApps()
	if d == nil {
		return map[string]any{"success": false, "error": "apps not configured"}
	}
	return apps.ToJSONMap(d.UpdateBundleFile(context.Background(), appName, filePath, content))
}

// ReadInnerAppBundleText reads scaffold-managed text files.
func (s *AppService) ReadInnerAppBundleText(appName, filePath string) map[string]any {
	d := s.rt.desktopApps()
	if d == nil {
		return map[string]any{"success": false, "error": "apps not configured"}
	}
	text, err := d.ReadBundleText(appName, filePath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "content": text}
}

// BuildInnerAppIteratively scaffolds, verifies, auto-repairs, and runs app_builder when LLM is configured.
func (s *AppService) BuildInnerAppIteratively(name, displayName, description, mode, workflowID, systemPrompt string, maxIterations int, overwrite bool) map[string]any {
	d := s.rt.desktopApps()
	if d == nil {
		return map[string]any{"success": false, "error": "apps not configured"}
	}
	opt := apps.IterativeBuildOptions{
		Name: name, DisplayName: displayName, Description: description,
		Mode: mode, WorkflowID: workflowID, SystemPrompt: systemPrompt,
		MaxIterations: maxIterations, Overwrite: overwrite,
	}
	iter := &apps.IterativeBuilder{Scaffolder: apps.NewScaffolder(s.rt.appsRoot, s.rt.appStore), Pinger: s.rt.appPinger()}
	if s.rt.agentRunner != nil && strings.TrimSpace(s.rt.LLMConfig().APIKey) != "" {
		return apps.ToJSONMap(s.rt.agentRunner.BuildInnerAppFull(context.Background(), s.rt.AgentLLMSettings(), "", iter, opt))
	}
	return apps.ToJSONMap(d.BuildIteratively(context.Background(), opt))
}
