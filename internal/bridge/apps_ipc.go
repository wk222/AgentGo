package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"agentgo/internal/apps"
)

// ListInnerApps returns registered inner apps for the desktop UI.
func (s *AppService) ListInnerApps(limit int) []map[string]any {
	if s.rt.appStore == nil {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	list, err := s.rt.appStore.List(context.Background(), limit)
	if err != nil {
		return nil
	}
	return marshalApps(list)
}

// GetInnerAppInfo returns one app definition.
func (s *AppService) GetInnerAppInfo(name string) map[string]any {
	ctx := context.Background()
	if s.rt.appStore == nil {
		return map[string]any{"success": false, "error": "no store"}
	}
	app, err := s.rt.appStore.GetByName(ctx, name)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	dir, ok := apps.ResolveBundleDir(s.rt.appsRoot, s.rt.workspace, app)
	files := []string{}
	if ok {
		files, _ = apps.ListBundleFiles(dir, 80)
	}
	b, _ := json.Marshal(app)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["success"] = true
	m["bundle_dir"] = dir
	m["bundle_ok"] = ok
	m["files"] = files
	m["has_ui"] = app.Kind == "ui" || app.BundlePath != ""
	m["manifest"] = apps.ManifestFor(app)
	return m
}

// GetInnerAppManifest returns the explicit UI/API action contract for one app.
func (s *AppService) GetInnerAppManifest(name string) map[string]any {
	ctx := context.Background()
	if s.rt.appStore == nil {
		return map[string]any{"success": false, "error": "no store"}
	}
	app, err := s.rt.appStore.GetByName(ctx, strings.TrimSpace(name))
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "manifest": apps.ManifestFor(app)}
}

// OpenInnerAppSession creates a nonce-bound host session for a UI iframe/window.
func (s *AppService) OpenInnerAppSession(name string) map[string]any {
	out, err := s.rt.openInnerAppSession(context.Background(), name)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return out
}

// InvokeInnerApp runs an app (same as agent tool invoke_inner_app).
func (s *AppService) InvokeInnerApp(name, input, capability string) map[string]any {
	ctx := context.Background()
	res := s.rt.InvokeInnerApp(ctx, name, input, capability, "", "")
	return invokeResultMap(res)
}

// CallInnerAppAPI is for UI bundles: action + JSON payload.
func (s *AppService) CallInnerAppAPI(name, action, payloadJSON string) map[string]any {
	ctx := context.Background()
	res := s.rt.InvokeInnerApp(ctx, name, "", "", action, payloadJSON)
	return invokeResultMap(res)
}

// CallInnerAppSessionAPI is the preferred UI bundle entry. It requires the
// per-host nonce issued by OpenInnerAppSession before dispatching an action.
func (s *AppService) CallInnerAppSessionAPI(name, sessionID, nonce, action, payloadJSON string) map[string]any {
	if err := s.rt.validateInnerAppSession(name, sessionID, nonce); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	ctx := context.Background()
	res := s.rt.InvokeInnerApp(ctx, name, "", "", action, payloadJSON)
	out := invokeResultMap(res)
	out["session_id"] = sessionID
	return out
}

// ReadInnerAppFile returns a bundle asset (base64 for binary-safe IPC).
func (s *AppService) ReadInnerAppFile(name, relPath string) map[string]any {
	ctx := context.Background()
	b, mime, err := s.rt.ReadInnerAppBundleFile(ctx, name, relPath)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if strings.HasPrefix(mime, "text/") || strings.Contains(mime, "javascript") || strings.Contains(mime, "json") {
		return map[string]any{"success": true, "text": string(b), "mime": mime}
	}
	return map[string]any{
		"success": true, "mime": mime,
		"base64": base64.StdEncoding.EncodeToString(b),
	}
}

// GetInnerAppPageHTML returns index.html with helpers injected.
func (s *AppService) GetInnerAppPageHTML(name string) map[string]any {
	ctx := context.Background()
	html, err := s.rt.InnerAppPageHTML(ctx, name)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "html": html, "app": name}
}

// SyncInnerAppsFromDisk rescans dataDir/apps and workspace/apps.
func (s *AppService) SyncInnerAppsFromDisk() map[string]any {
	return s.rt.SyncAppsFromDisk(context.Background())
}

// RegisterInnerAppUI registers or updates an app from the desktop (no agent required).
func (s *AppService) RegisterInnerAppUI(name, description, kind, workflowID, systemPrompt, bundlePath, exportsCSV string, enabled bool) map[string]any {
	if s.rt.appStore == nil {
		return map[string]any{"success": false, "error": "no store"}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return map[string]any{"success": false, "error": "name required"}
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "ui"
	}
	a := apps.InnerApp{
		Name: name, Description: description, Kind: kind,
		WorkflowID: workflowID, SystemPrompt: systemPrompt,
		BundlePath: bundlePath, Exports: apps.ParseExports(exportsCSV),
		Enabled: enabled,
	}
	if err := s.rt.appStore.Upsert(context.Background(), a); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	got, _ := s.rt.appStore.GetByName(context.Background(), name)
	s.rt.capBus.Register("app", got.Name, "inner", map[string]string{
		"kind": got.Kind, "app_id": got.ID,
	})
	b, _ := json.Marshal(got)
	var row map[string]any
	_ = json.Unmarshal(b, &row)
	return map[string]any{"success": true, "app": row}
}
