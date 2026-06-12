package bridge

import (
	"context"
	"encoding/json"
	"fmt"

	"agentgo/internal/apps"
)

// AppsRoot returns the directory for on-disk app bundles.
func (r *Runtime) AppsRoot() string {
	return r.appsRoot
}

// AppStore returns the inner apps store.
func (r *Runtime) AppStore() *apps.Store {
	return r.appStore
}

// InvokeInnerApp is the unified entry for tools, UI IPC, and gateway.
func (r *Runtime) InvokeInnerApp(ctx context.Context, name, input, capability, action, payloadJSON string) apps.InvokeResult {
	if r.appStore == nil {
		return apps.InvokeResult{Error: "app store unavailable"}
	}
	app, err := r.appStore.GetByName(ctx, name)
	if err != nil {
		return apps.InvokeResult{Error: "app not found: " + name}
	}
	runner := &innerAppRunnerAdapter{rt: r}
	res := apps.Invoke(ctx, app, runner, apps.InvokeRequest{
		Input: input, Capability: capability, Action: action, PayloadJSON: payloadJSON,
	})
	if res.Error != "" && r.capBus != nil {
		r.capBus.PublishAppRuntimeError(name, res.Error)
	}
	return res
}

type innerAppRunnerAdapter struct{ rt *Runtime }

func (a *innerAppRunnerAdapter) RunWorkflow(ctx context.Context, workflowID, input string) (string, error) {
	return a.rt.RunWorkflow(ctx, workflowID, input)
}

func (a *innerAppRunnerAdapter) RunAgentPrompt(ctx context.Context, sessionID, systemPrompt, userInput string) (string, error) {
	return a.rt.RunAgentPrompt(ctx, sessionID, systemPrompt, userInput)
}

func invokeResultMap(res apps.InvokeResult) map[string]any {
	out := map[string]any{"success": res.Error == "", "output": res.Output, "kind": res.Kind}
	if res.Error != "" {
		out["error"] = res.Error
		out["success"] = false
	}
	if res.Meta != nil {
		out["meta"] = res.Meta
	}
	return out
}

func (r *Runtime) ReadInnerAppBundleFile(ctx context.Context, appName, relPath string) (content []byte, mime string, err error) {
	if r.appStore == nil {
		return nil, "", fmt.Errorf("app store unavailable")
	}
	app, err := r.appStore.GetByName(ctx, appName)
	if err != nil {
		return nil, "", err
	}
	dir, ok := apps.ResolveBundleDir(r.appsRoot, r.workspace, app)
	if !ok {
		return nil, "", fmt.Errorf("bundle not found for app %q", appName)
	}
	return apps.ReadBundleFile(dir, relPath)
}

func (r *Runtime) InnerAppPageHTML(ctx context.Context, appName string) (string, error) {
	return apps.PageHTML(ctx, r.appStore, r.appsRoot, r.workspace, appName)
}

func (r *Runtime) SyncAppsFromDisk(ctx context.Context) map[string]any {
	if r.appStore == nil {
		return map[string]any{"success": false, "error": "no store"}
	}
	n, err := apps.ScanRoots(ctx, r.appStore, r.appsRoot, r.workspaceAppsDir())
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	return map[string]any{"success": true, "imported": n}
}

func (r *Runtime) workspaceAppsDir() string {
	if r.workspace == "" {
		return ""
	}
	return r.workspace + "/apps"
}

func marshalApps(list []apps.InnerApp) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, a := range list {
		b, _ := json.Marshal(a)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["has_ui"] = a.Kind == "ui" || a.BundlePath != ""
		m["manifest"] = apps.ManifestFor(a)
		out = append(out, m)
	}
	return out
}
