// Package agentpack packages agent-generated artifacts (workflows, dynamic
// tools, inner apps) into a portable, offline ".agentpack" archive so they can
// be shared between AgentGo users. A pack is a plain ZIP with a manifest.json.
package agentpack

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentgo/internal/apps"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
)

// FormatVersion is the on-disk schema version of the .agentpack manifest.
const FormatVersion = 1

// Manifest describes the contents of a .agentpack archive.
type Manifest struct {
	FormatVersion int            `json:"format_version"`
	Producer      string         `json:"producer"`
	Title         string         `json:"title,omitempty"`
	CreatedAt     int64          `json:"created_at"`
	Items         []ManifestItem `json:"items"`
}

// ManifestItem is one shareable artifact inside the pack.
type ManifestItem struct {
	Type        string `json:"type"` // workflow | tool | innerapp
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`       // location within the archive
	Executable  bool   `json:"executable"` // true when it carries runnable code (tools)
	Notes       string `json:"notes,omitempty"`
}

// Engine packages and installs shareable artifacts against the local stores.
type Engine struct {
	WF        *workflow.Store
	Dyn       *tools.DynamicStore
	Apps      *apps.Store
	Reg       *tools.Registry // optional: live-register imported tools
	AppsRoot  string
	Workspace string
	OutDir    string // default directory for exported packs
}

// ExportRequest selects the artifacts to bundle.
type ExportRequest struct {
	Title               string
	Workflows           []string // ids or names
	Tools               []string // dynamic tool names
	InnerApps           []string // inner app names
	IncludeDependencies bool     // pull an inner app's referenced workflow too
	OutPath             string   // optional explicit output path
}

// ImportResult reports what an import did (or what it is waiting to confirm).
type ImportResult struct {
	Installed     []ManifestItem `json:"installed"`
	Skipped       []SkippedItem  `json:"skipped"`
	NeedConfirm   bool           `json:"need_confirm"`
	HasExecutable bool           `json:"has_executable"`
	Message       string         `json:"message"`
}

// SkippedItem records an artifact that was not installed and why.
type SkippedItem struct {
	ManifestItem
	Reason string `json:"reason"`
}

// Export writes the selected artifacts into a .agentpack file and returns its path.
func (e *Engine) Export(ctx context.Context, req ExportRequest) (string, *Manifest, error) {
	if e == nil {
		return "", nil, fmt.Errorf("agentpack: engine not configured")
	}
	man := &Manifest{FormatVersion: FormatVersion, Producer: "AgentGo", Title: strings.TrimSpace(req.Title), CreatedAt: time.Now().Unix()}

	outPath := strings.TrimSpace(req.OutPath)
	if outPath == "" {
		base := safeFile(req.Title)
		if base == "" {
			base = "agentpack"
		}
		dir := e.OutDir
		if dir == "" {
			dir = "."
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", nil, err
		}
		outPath = filepath.Join(dir, fmt.Sprintf("%s-%d.agentpack", base, time.Now().Unix()))
	} else if !strings.HasSuffix(outPath, ".agentpack") {
		outPath += ".agentpack"
	}

	f, err := os.Create(outPath)
	if err != nil {
		return "", nil, err
	}
	zw := zip.NewWriter(f)

	wfSeen := map[string]bool{}
	toolSeen := map[string]bool{}

	addWorkflow := func(idOrName string) error {
		def, err := e.resolveWorkflow(idOrName)
		if err != nil {
			return err
		}
		if wfSeen[def.ID] {
			return nil
		}
		wfSeen[def.ID] = true
		p := "workflows/" + safeFile(def.ID) + ".json"
		if err := writeJSON(zw, p, def); err != nil {
			return err
		}
		man.Items = append(man.Items, ManifestItem{Type: "workflow", Name: def.Name, Description: def.Description, Path: p})
		return nil
	}
	addTool := func(name string) error {
		def, err := e.Dyn.Get(name)
		if err != nil {
			return fmt.Errorf("tool %q: %w", name, err)
		}
		if toolSeen[def.Name] {
			return nil
		}
		toolSeen[def.Name] = true
		p := "tools/" + safeFile(def.Name) + ".json"
		if err := writeJSON(zw, p, def); err != nil {
			return err
		}
		man.Items = append(man.Items, ManifestItem{Type: "tool", Name: def.Name, Description: def.Description, Path: p, Executable: true, Notes: "contains executable code; review before importing"})
		return nil
	}
	addInnerApp := func(name string) error {
		app, err := e.Apps.GetByName(ctx, name)
		if err != nil {
			return fmt.Errorf("innerapp %q: %w", name, err)
		}
		base := "apps/" + safeFile(app.Name)
		meta := app
		meta.BundlePath = app.Name // recipient re-roots the bundle under this name
		if err := writeJSON(zw, base+"/meta.json", meta); err != nil {
			return err
		}
		man.Items = append(man.Items, ManifestItem{Type: "innerapp", Name: app.Name, Description: app.Description, Path: base + "/meta.json"})
		if dir, ok := apps.ResolveBundleDir(e.AppsRoot, e.Workspace, app); ok {
			if err := addDirToZip(zw, dir, base+"/bundle"); err != nil {
				return err
			}
		}
		if req.IncludeDependencies && strings.TrimSpace(app.WorkflowID) != "" {
			_ = addWorkflow(app.WorkflowID) // dependency is best-effort
		}
		return nil
	}

	abort := func(err error) (string, *Manifest, error) {
		_ = zw.Close()
		_ = f.Close()
		_ = os.Remove(outPath)
		return "", nil, err
	}

	for _, id := range req.Workflows {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if err := addWorkflow(id); err != nil {
			return abort(err)
		}
	}
	for _, name := range req.Tools {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if err := addTool(name); err != nil {
			return abort(err)
		}
	}
	for _, name := range req.InnerApps {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if err := addInnerApp(name); err != nil {
			return abort(err)
		}
	}

	if len(man.Items) == 0 {
		return abort(fmt.Errorf("agentpack: nothing to export (no valid workflows/tools/innerapps selected)"))
	}
	if err := writeJSON(zw, "manifest.json", man); err != nil {
		return abort(err)
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		return "", nil, err
	}
	return outPath, man, nil
}

// Inspect reads only the manifest of a pack without installing anything.
func Inspect(path string) (*Manifest, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if zf.Name == "manifest.json" {
			var m Manifest
			if err := readZipJSON(zf, &m); err != nil {
				return nil, err
			}
			return &m, nil
		}
	}
	return nil, fmt.Errorf("agentpack: manifest.json missing (not a valid .agentpack)")
}

// Import installs the artifacts from a pack. If the pack carries executable tool
// code and confirm is false, it returns a preview (NeedConfirm) without
// installing. Existing artifacts (workflow by id, tool/inner app by name) are
// skipped unless overwrite is true.
func (e *Engine) Import(ctx context.Context, path string, confirm, overwrite bool) (*ImportResult, error) {
	if e == nil {
		return nil, fmt.Errorf("agentpack: engine not configured")
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	files := make(map[string]*zip.File, len(zr.File))
	for _, zf := range zr.File {
		files[zf.Name] = zf
	}
	mf, ok := files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("agentpack: manifest.json missing (not a valid .agentpack)")
	}
	var man Manifest
	if err := readZipJSON(mf, &man); err != nil {
		return nil, err
	}

	res := &ImportResult{}
	skip := func(it ManifestItem, reason string) {
		res.Skipped = append(res.Skipped, SkippedItem{ManifestItem: it, Reason: reason})
	}

	res.HasExecutable = hasExecutableItems(man.Items)
	if res.HasExecutable && !confirm {
		res.NeedConfirm = true
		for _, it := range man.Items {
			skip(it, "needs confirm=true (pack contains executable tool code)")
		}
		res.Message = "this pack contains executable tool code — review the items, then re-run with confirm=true to install"
		return res, nil
	}

	for _, it := range man.Items {
		zf, ok := files[it.Path]
		if !ok {
			skip(it, "payload missing from archive")
			continue
		}
		switch it.Type {
		case "workflow":
			var def workflow.Definition
			if err := readZipJSON(zf, &def); err != nil {
				skip(it, "invalid workflow payload")
				continue
			}
			if !overwrite {
				if _, err := e.WF.Get(def.ID); err == nil {
					skip(it, "a workflow with this id already exists (set overwrite=true to replace)")
					continue
				}
			}
			if def.CreatedAt == 0 {
				def.CreatedAt = time.Now().Unix()
			}
			if err := e.WF.Save(def); err != nil {
				skip(it, err.Error())
				continue
			}
			res.Installed = append(res.Installed, it)
		case "tool":
			var def tools.DynamicToolDef
			if err := readZipJSON(zf, &def); err != nil {
				skip(it, "invalid tool payload")
				continue
			}
			if !overwrite {
				if _, err := e.Dyn.Get(def.Name); err == nil {
					skip(it, "a tool named "+def.Name+" already exists (set overwrite=true to replace)")
					continue
				}
			}
			if def.CreatedAt == 0 {
				def.CreatedAt = time.Now().Unix()
			}
			if err := e.Dyn.Save(def); err != nil {
				skip(it, err.Error())
				continue
			}
			if e.Reg != nil {
				_ = e.Reg.RegisterDynamicTool(def)
			}
			res.Installed = append(res.Installed, it)
		case "innerapp":
			var app apps.InnerApp
			if err := readZipJSON(zf, &app); err != nil {
				skip(it, "invalid inner app metadata")
				continue
			}
			name := strings.TrimSpace(app.Name)
			if name == "" || safeFile(name) == "" {
				skip(it, "inner app has an invalid name")
				continue
			}
			if !overwrite {
				if _, err := e.Apps.GetByName(ctx, name); err == nil {
					skip(it, "an inner app named "+name+" already exists (set overwrite=true to replace)")
					continue
				}
			}
			if err := e.installInnerApp(ctx, it, app, files); err != nil {
				skip(it, err.Error())
				continue
			}
			res.Installed = append(res.Installed, it)
		default:
			skip(it, "unknown item type: "+it.Type)
		}
	}
	res.Message = fmt.Sprintf("installed %d item(s), skipped %d", len(res.Installed), len(res.Skipped))
	return res, nil
}

// hasExecutableItems trusts the actual item type over the self-declared flag:
// any tool item carries runnable code and must clear the confirmation gate.
func hasExecutableItems(items []ManifestItem) bool {
	for _, it := range items {
		if it.Type == "tool" || it.Executable {
			return true
		}
	}
	return false
}

// installInnerApp extracts an inner app's UI bundle (if any) into a local,
// sandboxed directory and persists its metadata. The pack-provided BundlePath
// is never trusted: it is re-rooted to a local controlled name (or cleared when
// the pack ships no bundle), so an imported app can never point the recipient at
// an arbitrary host directory.
func (e *Engine) installInnerApp(ctx context.Context, it ManifestItem, app apps.InnerApp, files map[string]*zip.File) error {
	localName := safeFile(app.Name)
	if localName == "" {
		return fmt.Errorf("invalid inner app name")
	}
	bundlePrefix := strings.TrimSuffix(it.Path, "/meta.json") + "/bundle/"
	destDir := filepath.Join(e.AppsRoot, localName)
	wrote := false
	for name, f := range files {
		if !strings.HasPrefix(name, bundlePrefix) {
			continue
		}
		rel := strings.TrimPrefix(name, bundlePrefix)
		if rel == "" || strings.HasSuffix(name, "/") {
			continue
		}
		dest, err := safeJoin(destDir, rel)
		if err != nil {
			return err
		}
		if err := extractZipFile(f, e.AppsRoot, dest); err != nil {
			return err
		}
		wrote = true
	}
	if wrote {
		app.BundlePath = localName
	} else {
		app.BundlePath = ""
	}
	if app.CreatedAt == 0 {
		app.CreatedAt = time.Now().Unix()
	}
	return e.Apps.Upsert(ctx, app)
}

func (e *Engine) resolveWorkflow(idOrName string) (workflow.Definition, error) {
	idOrName = strings.TrimSpace(idOrName)
	if def, err := e.WF.Get(idOrName); err == nil {
		return def, nil
	}
	list, err := e.WF.List()
	if err != nil {
		return workflow.Definition{}, err
	}
	for _, d := range list {
		if strings.EqualFold(d.Name, idOrName) {
			return d, nil
		}
	}
	return workflow.Definition{}, fmt.Errorf("workflow %q not found", idOrName)
}

// --- zip helpers ---

func writeJSON(zw *zip.Writer, name string, v any) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func readZipJSON(zf *zip.File, v any) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return json.NewDecoder(rc).Decode(v)
}

func addDirToZip(zw *zip.Writer, srcDir, destPrefix string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(destPrefix + "/" + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(w, src)
		return err
	})
}

func extractZipFile(zf *zip.File, root, dest string) error {
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	// Defense-in-depth against symlink escape: resolve the real parent dir and
	// require it to stay under the real root. A pack cannot ship symlinks (we
	// only ever write regular files), but a pre-existing symlink under the apps
	// root must not let an import write outside it.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	if realParent != realRoot && !strings.HasPrefix(realParent, realRoot+string(os.PathSeparator)) {
		return fmt.Errorf("agentpack: refusing to write outside apps root: %q", dest)
	}
	// Never overwrite through an existing symlink at the destination itself.
	if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("agentpack: refusing to overwrite symlink: %q", dest)
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// safeJoin joins rel onto base, rejecting paths that escape base (zip-slip guard).
func safeJoin(base, rel string) (string, error) {
	dest := filepath.Join(base, filepath.FromSlash(rel))
	cleanBase := filepath.Clean(base) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(dest)+string(os.PathSeparator), cleanBase) {
		return "", fmt.Errorf("agentpack: illegal path in pack: %q", rel)
	}
	return dest, nil
}

// safeFile reduces a string to a filesystem/zip-safe token. It returns "" for
// path-traversal tokens (".", "..", or any all-dots string) so callers can
// reject them — this is the boundary that keeps untrusted pack names from
// escaping the apps root.
func safeFile(s string) string {
	s = strings.TrimSpace(s)
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
	if strings.Trim(mapped, ".") == "" {
		return ""
	}
	return mapped
}
