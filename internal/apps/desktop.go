package apps

import (
	"context"
	"encoding/json"
	"strings"
)

// AppRegistrar is called after a successful scaffold / iterative build (e.g. capability bus).
type AppRegistrar func(InnerApp)

// DesktopService exposes inner-app desktop operations (domain layer; bridge only adapts to IPC).
type DesktopService struct {
	Root       string
	Store      *Store
	Pinger     AppPinger
	OnRegister AppRegistrar
}

func (d *DesktopService) scaffolder() *Scaffolder {
	return NewScaffolder(d.Root, d.Store)
}

func (d *DesktopService) builder() *IterativeBuilder {
	return &IterativeBuilder{Scaffolder: d.scaffolder(), Pinger: d.Pinger}
}

// Scaffold creates bundle files and upserts the store.
func (d *DesktopService) Scaffold(ctx context.Context, opt ScaffoldOptions) ScaffoldResult {
	if d == nil || d.Root == "" {
		return ScaffoldResult{Success: false, Error: "apps not configured"}
	}
	res, _ := d.scaffolder().Scaffold(ctx, opt)
	d.maybeRegister(res.AppName, res.Success)
	return res
}

// UpdateBundleFile writes a bundle file with validation.
func (d *DesktopService) UpdateBundleFile(ctx context.Context, appName, filePath, content string) UpdateFileResult {
	if d == nil || d.Root == "" {
		return UpdateFileResult{Success: false, Error: "apps not configured"}
	}
	res, _ := d.scaffolder().UpdateFile(ctx, appName, filePath, content)
	return res
}

// ReadBundleText reads a text file from the bundle.
func (d *DesktopService) ReadBundleText(appName, filePath string) (string, error) {
	if d == nil || d.Root == "" {
		return "", errAppsNotConfigured()
	}
	return d.scaffolder().ReadFile(appName, filePath)
}

// BuildIteratively runs scaffold + verify + auto-fix loop.
func (d *DesktopService) BuildIteratively(ctx context.Context, opt IterativeBuildOptions) IterativeBuildResult {
	if d == nil || d.Root == "" {
		return IterativeBuildResult{Success: false, Error: "apps not configured"}
	}
	res := d.builder().Build(ctx, opt)
	d.maybeRegister(res.AppName, res.Success)
	return res
}

func (d *DesktopService) maybeRegister(appName string, ok bool) {
	if !ok || d.OnRegister == nil || d.Store == nil || strings.TrimSpace(appName) == "" {
		return
	}
	app, err := d.Store.GetByName(context.Background(), appName)
	if err == nil {
		d.OnRegister(app)
	}
}

func errAppsNotConfigured() error {
	return &desktopConfigError{}
}

type desktopConfigError struct{}

func (e *desktopConfigError) Error() string { return "apps not configured" }

// ParseExportsCSV splits comma-separated export names.
func ParseExportsCSV(exportsCSV string) []string {
	var exports []string
	for _, e := range strings.Split(exportsCSV, ",") {
		if t := strings.TrimSpace(e); t != "" {
			exports = append(exports, t)
		}
	}
	return exports
}

// ToJSONMap marshals a struct into map[string]any for Wails IPC.
func ToJSONMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
