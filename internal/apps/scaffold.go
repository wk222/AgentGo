package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var validAppNameRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

// ScaffoldOptions configures PyBot-style app creation.
type ScaffoldOptions struct {
	Name         string
	DisplayName  string
	Description  string
	Mode         string // chat | static | workflow
	WorkflowID   string
	SystemPrompt string
	Exports      []string
	Overwrite    bool
}

// ScaffoldResult is returned to agents and IPC.
type ScaffoldResult struct {
	Success   bool     `json:"success"`
	AppName   string   `json:"app_name,omitempty"`
	Mode      string   `json:"mode,omitempty"`
	Path      string   `json:"path,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	Files     []string `json:"files_written,omitempty"`
	Message   string   `json:"message,omitempty"`
	Error     string   `json:"error,omitempty"`
	OpenHint  string   `json:"open_hint,omitempty"`
}

// Scaffolder writes app bundles under appsRoot and syncs SQLite store.
type Scaffolder struct {
	AppsRoot string
	Store    *Store
}

func NewScaffolder(appsRoot string, store *Store) *Scaffolder {
	return &Scaffolder{AppsRoot: appsRoot, Store: store}
}

func (s *Scaffolder) Scaffold(ctx context.Context, opt ScaffoldOptions) (ScaffoldResult, error) {
	name := strings.TrimSpace(opt.Name)
	if !validAppNameRe.MatchString(name) {
		return ScaffoldResult{Success: false, Error: "invalid app name (use letters, digits, _ -)"}, fmt.Errorf("invalid name")
	}
	mode := strings.ToLower(strings.TrimSpace(opt.Mode))
	if mode == "" {
		mode = "static"
	}
	appDir := filepath.Join(s.AppsRoot, name)
	if st, err := os.Stat(appDir); err == nil && st.IsDir() && !opt.Overwrite {
		return ScaffoldResult{Success: false, Error: "app already exists; set overwrite=true to replace scaffold"}, fmt.Errorf("exists")
	}
	tpl, err := templateForMode(mode, name, opt.DisplayName, opt.Description, opt.WorkflowID)
	if err != nil {
		return ScaffoldResult{Success: false, Error: err.Error()}, err
	}
	if err := os.MkdirAll(filepath.Join(appDir, "static"), 0o755); err != nil {
		return ScaffoldResult{Success: false, Error: err.Error()}, err
	}
	files := []string{AppEntryFile, "static/style.css", "static/app.js", AppMetadataFile}
	if err := writeTextFile(filepath.Join(appDir, AppEntryFile), tpl.html); err != nil {
		return ScaffoldResult{Success: false, Error: err.Error()}, err
	}
	if err := writeTextFile(filepath.Join(appDir, "static", "style.css"), tpl.css); err != nil {
		return ScaffoldResult{Success: false, Error: err.Error()}, err
	}
	if err := writeTextFile(filepath.Join(appDir, "static", "app.js"), tpl.js); err != nil {
		return ScaffoldResult{Success: false, Error: err.Error()}, err
	}

	kind := "ui"
	wf := strings.TrimSpace(opt.WorkflowID)
	sp := strings.TrimSpace(opt.SystemPrompt)
	exports := opt.Exports
	if len(exports) == 0 {
		exports = []string{"ping", "echo"}
		if mode == "workflow" {
			exports = append(exports, "workflow_run")
		}
	}
	if mode == "workflow" {
		if wf == "" {
			return ScaffoldResult{Success: false, Error: "workflow_id required"}, fmt.Errorf("workflow_id")
		}
	}

	meta := diskAppMeta{
		Name: name, DisplayName: firstNonEmpty(opt.DisplayName, name),
		Description: opt.Description, Kind: kind, Mode: mode,
		WorkflowID: wf, WorkflowBind: wf, SystemPrompt: sp,
		Exports: exports, Enabled: boolPtr(true), BundlePath: name,
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := writeTextFile(filepath.Join(appDir, AppMetadataFile), string(mb)); err != nil {
		return ScaffoldResult{Success: false, Error: err.Error()}, err
	}

	inner := InnerApp{
		Name: name, Description: firstNonEmpty(opt.DisplayName, opt.Description),
		Kind: kind, WorkflowID: wf, SystemPrompt: sp,
		BundlePath: name, Exports: exports, Enabled: true,
		Metadata: map[string]string{"scaffold_mode": mode, "created_at": fmt.Sprintf("%d", time.Now().Unix())},
	}
	if s.Store != nil {
		if err := s.Store.Upsert(ctx, inner); err != nil {
			return ScaffoldResult{Success: false, Error: err.Error()}, err
		}
	}

	return ScaffoldResult{
		Success:  true,
		AppName:  name,
		Mode:     mode,
		Path:     appDir,
		Kind:     kind,
		Files:    files,
		OpenHint: "桌面侧栏「应用」→ 打开 " + name,
		Message:  "scaffold complete; open 应用 panel or invoke_inner_app",
	}, nil
}

// UpdateFileResult holds write + validation outcome.
type UpdateFileResult struct {
	Success            bool        `json:"success"`
	File               string      `json:"file,omitempty"`
	Size               int         `json:"size,omitempty"`
	Validation         []FileIssue `json:"validation,omitempty"`
	HasCriticalIssues  bool        `json:"has_critical_issues,omitempty"`
	ActionRequired     string      `json:"action_required,omitempty"`
	Error              string      `json:"error,omitempty"`
}

func (s *Scaffolder) UpdateFile(ctx context.Context, appName, relPath, content string) (UpdateFileResult, error) {
	appName = strings.TrimSpace(appName)
	if appName == "" {
		return UpdateFileResult{Success: false, Error: "app_name required"}, fmt.Errorf("app_name")
	}
	appDir := filepath.Join(s.AppsRoot, appName)
	if _, err := os.Stat(appDir); err != nil {
		return UpdateFileResult{Success: false, Error: "app directory not found: " + appName}, err
	}
	full, rel, err := safeAppFilePath(appDir, relPath)
	if err != nil {
		return UpdateFileResult{Success: false, Error: err.Error()}, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return UpdateFileResult{Success: false, Error: err.Error()}, err
	}
	if err := writeTextFile(full, content); err != nil {
		return UpdateFileResult{Success: false, Error: err.Error()}, err
	}
	issues := validateAppFile(rel, content)
	res := UpdateFileResult{
		Success: true, File: rel, Size: len(content), Validation: issues,
	}
	for _, iss := range issues {
		if iss.Severity == "critical" {
			res.HasCriticalIssues = true
			res.ActionRequired = "修复 critical 问题后再次调用 update_inner_app_file"
			break
		}
	}
	if rel == AppMetadataFile && s.Store != nil {
		var dm diskAppMeta
		if json.Unmarshal([]byte(content), &dm) == nil {
			_, _ = ScanRoots(ctx, s.Store, s.AppsRoot)
		}
	}
	return res, nil
}

func (s *Scaffolder) ReadFile(appName, relPath string) (string, error) {
	appDir := filepath.Join(s.AppsRoot, strings.TrimSpace(appName))
	full, _, err := safeAppFilePath(appDir, relPath)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Scaffolder) ListFiles(appName string, max int) ([]string, error) {
	dir := filepath.Join(s.AppsRoot, strings.TrimSpace(appName))
	return ListBundleFiles(dir, max)
}

func safeAppFilePath(appDir, relPath string) (fullPath, rel string, err error) {
	rel = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(relPath), "/"))
	if rel == "" {
		return "", "", fmt.Errorf("file_path required")
	}
	clean := filepath.Clean(rel)
	if strings.HasPrefix(clean, "..") {
		return "", "", fmt.Errorf("path traversal not allowed")
	}
	fullPath = filepath.Join(appDir, clean)
	rel = clean
	return fullPath, rel, nil
}

func writeTextFile(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
