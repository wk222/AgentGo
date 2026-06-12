package apps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// diskAppMeta is app.json on disk (PyBot-compatible subset).
type diskAppMeta struct {
	Name         string   `json:"name"`
	DisplayName  string   `json:"display_name"`
	Description  string   `json:"description"`
	Kind         string   `json:"kind"`
	Mode         string   `json:"mode"`
	WorkflowID   string   `json:"workflow_id"`
	WorkflowBind string   `json:"workflow_binding"`
	SystemPrompt string   `json:"system_prompt"`
	SystemOver   string   `json:"system_prompt_override"`
	Exports      []string `json:"exports"`
	Enabled      *bool    `json:"enabled"`
	EntryPoint   string   `json:"entry_point"`
	BundlePath   string   `json:"bundle_path"`
}

// ScanRoots imports app.json from each apps directory into the store.
func ScanRoots(ctx context.Context, store *Store, roots ...string) (int, error) {
	if store == nil {
		return 0, nil
	}
	n := 0
	seen := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() || strings.HasPrefix(ent.Name(), ".") {
				continue
			}
			metaPath := filepath.Join(root, ent.Name(), AppMetadataFile)
			if _, err := os.Stat(metaPath); err != nil {
				// folder without app.json: register as ui bundle if index.html exists
				idx := filepath.Join(root, ent.Name(), AppEntryFile)
				if _, err2 := os.Stat(idx); err2 != nil {
					continue
				}
				a := InnerApp{
					Name: ent.Name(), Kind: "ui", BundlePath: ent.Name(),
					Enabled: true, Description: "discovered app bundle",
				}
				if seen[a.Name] {
					continue
				}
				seen[a.Name] = true
				if err := store.Upsert(ctx, a); err == nil {
					n++
				}
				continue
			}
			b, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var dm diskAppMeta
			if json.Unmarshal(b, &dm) != nil {
				continue
			}
			name := strings.TrimSpace(dm.Name)
			if name == "" {
				name = ent.Name()
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			kind := strings.ToLower(strings.TrimSpace(dm.Kind))
			if kind == "" {
				if _, err := os.Stat(filepath.Join(root, ent.Name(), AppEntryFile)); err == nil {
					kind = "ui"
				} else {
					kind = "workflow"
				}
			}
			wf := dm.WorkflowID
			if wf == "" {
				wf = dm.WorkflowBind
			}
			sp := dm.SystemPrompt
			if sp == "" {
				sp = dm.SystemOver
			}
			enabled := true
			if dm.Enabled != nil {
				enabled = *dm.Enabled
			}
			bp := dm.BundlePath
			if bp == "" {
				bp = ent.Name()
			}
			a := InnerApp{
				Name: name, Description: firstNonEmpty(dm.DisplayName, dm.Description),
				Kind: kind, WorkflowID: wf, SystemPrompt: sp,
				BundlePath: bp, Exports: dm.Exports, Enabled: enabled,
			}
			if err := store.Upsert(ctx, a); err == nil {
				n++
			}
		}
	}
	return n, nil
}

func firstNonEmpty(parts ...string) string {
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// EnsureDemoApp writes a minimal UI demo under appsRoot if missing.
func EnsureDemoApp(appsRoot string) error {
	dir := filepath.Join(appsRoot, "demo")
	idx := filepath.Join(dir, AppEntryFile)
	if _, err := os.Stat(idx); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "static"), 0o755); err != nil {
		return err
	}
	meta := diskAppMeta{
		Name: "demo", DisplayName: "AgentGo Demo App", Kind: "ui",
		Description: "示例内置应用：演示 UI + apiCall",
		Exports:     []string{"ping", "echo"},
		Enabled:     boolPtr(true),
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, AppMetadataFile), mb, 0o644); err != nil {
		return err
	}
	html := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <title>AgentGo Demo App</title>
  <link rel="stylesheet" href="static/style.css" />
  <script src="/agentgo-app-helpers.js"></script>
</head>
<body>
  <h1>AgentGo 内置应用</h1>
  <p>通过 <code>agentGo.apiCall</code> 调用宿主；Agent 可用 <code>invoke_inner_app</code> / <code>invoke_app_capability</code>。</p>
  <button id="btn">Ping</button>
  <pre id="out"></pre>
  <script src="static/app.js"></script>
</body>
</html>`
	css := `body { font-family: system-ui; padding: 1rem; max-width: 640px; }
button { padding: 0.5rem 1rem; cursor: pointer; }
pre { background: #1e1e2e; color: #cdd6f4; padding: 1rem; border-radius: 8px; }`
	js := `document.getElementById('btn').onclick = async () => {
  const out = document.getElementById('out');
  out.textContent = 'calling...';
  try {
    const r = await agentGo.apiCall('ping', { t: Date.now() });
    out.textContent = JSON.stringify(r, null, 2);
  } catch (e) { out.textContent = e.message; }
};`
	if err := os.WriteFile(idx, []byte(html), 0o644); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(dir, "static", "style.css"), []byte(css), 0o644)
	return os.WriteFile(filepath.Join(dir, "static", "app.js"), []byte(js), 0o644)
}

func boolPtr(v bool) *bool { return &v }
