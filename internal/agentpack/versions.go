package agentpack

import (
	"agentgo/internal/apps"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
	"archive/zip"
	"context"
	"fmt"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Version struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	CreatedAt int64          `json:"created_at"`
	Items     []ManifestItem `json:"items"`
}
type Validation struct {
	OK     bool     `json:"ok"`
	Issues []string `json:"issues"`
	Scope  string   `json:"scope"`
}

var versionsMu sync.Mutex

func (e *Engine) versionPath(id string) (string, error) {
	if e == nil || e.VersionsDir == "" {
		return "", fmt.Errorf("extension versions unavailable")
	}
	if _, err := uuid.Parse(id); err != nil {
		return "", fmt.Errorf("invalid version ID")
	}
	return filepath.Join(e.VersionsDir, id+".agentpack"), nil
}
func (e *Engine) Versions() ([]Version, error) {
	out := []Version{}
	if e == nil || e.VersionsDir == "" {
		return out, fmt.Errorf("extension versions unavailable")
	}
	entries, err := os.ReadDir(e.VersionsDir)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".agentpack")
		p, err := e.versionPath(id)
		if err != nil || entry.IsDir() {
			continue
		}
		man, err := Inspect(p)
		if err != nil {
			continue
		}
		out = append(out, Version{ID: id, Title: man.Title, CreatedAt: man.CreatedAt, Items: man.Items})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out, nil
}
func (e *Engine) Snapshot(ctx context.Context, req ExportRequest) (Version, error) {
	versionsMu.Lock()
	defer versionsMu.Unlock()
	return e.snapshot(ctx, req)
}
func (e *Engine) snapshot(ctx context.Context, req ExportRequest) (Version, error) {
	id := uuid.NewString()
	p, err := e.versionPath(id)
	if err != nil {
		return Version{}, err
	}
	if err = os.MkdirAll(e.VersionsDir, 0700); err != nil {
		return Version{}, err
	}
	req.OutPath = p
	_, man, err := e.Export(ctx, req)
	if err != nil {
		return Version{}, err
	}
	report := Validate(p)
	if !report.OK {
		_ = os.Remove(p)
		return Version{}, fmt.Errorf("snapshot validation failed: %s", strings.Join(report.Issues, "; "))
	}
	return Version{ID: id, Title: man.Title, CreatedAt: man.CreatedAt, Items: man.Items}, nil
}

// Restore captures current versions first. Partial imports are reported as errors.
func (e *Engine) Restore(ctx context.Context, id string) (*ImportResult, error) {
	versionsMu.Lock()
	defer versionsMu.Unlock()
	p, err := e.versionPath(id)
	if err != nil {
		return nil, err
	}
	report := Validate(p)
	if !report.OK {
		return nil, fmt.Errorf("invalid version: %s", strings.Join(report.Issues, "; "))
	}
	req, err := e.currentSelection(p)
	if err != nil {
		return nil, err
	}
	req.Title = "回退前自动备份"
	var backup Version
	if len(req.Workflows)+len(req.Tools)+len(req.InnerApps)+len(req.Skills) > 0 {
		backup, err = e.snapshot(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("backup failed; restore not started: %w", err)
		}
	}
	res, err := e.Import(ctx, p, true, true)
	if err != nil || res == nil || len(res.Skipped) > 0 {
		recovery := "no previous artifacts"
		if backup.ID != "" {
			bp, _ := e.versionPath(backup.ID)
			r, re := e.Import(ctx, bp, true, true)
			recovery = fmt.Sprintf("backup=%s error=%v", backup.ID, re)
			if r != nil {
				recovery += fmt.Sprintf(" skipped=%d", len(r.Skipped))
			}
		}
		return res, fmt.Errorf("restore incomplete: %v; recovery: %s", err, recovery)
	}
	return res, nil
}
func (e *Engine) currentSelection(p string) (ExportRequest, error) {
	req := ExportRequest{}
	zr, err := zip.OpenReader(p)
	if err != nil {
		return req, err
	}
	defer zr.Close()
	man, err := Inspect(p)
	if err != nil {
		return req, err
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	for _, it := range man.Items {
		switch it.Type {
		case "workflow":
			var d workflow.Definition
			if err := readZipJSON(files[it.Path], &d); err != nil {
				return req, err
			}
			if e.WF != nil {
				if _, err := e.WF.Get(d.ID); err == nil {
					req.Workflows = append(req.Workflows, d.ID)
				}
			}
		case "tool":
			if e.Dyn != nil {
				if _, err := e.Dyn.Get(it.Name); err == nil {
					req.Tools = append(req.Tools, it.Name)
				}
			}
		case "innerapp":
			if e.Apps != nil {
				if _, err := e.Apps.GetByName(context.Background(), it.Name); err == nil {
					req.InnerApps = append(req.InnerApps, it.Name)
				}
			}
		case "skill":
			if _, err := os.Stat(filepath.Join(e.Workspace, "skills", it.Name, "SKILL.md")); err == nil {
				req.Skills = append(req.Skills, it.Name)
			}
		}
	}
	return req, nil
}

// Validate checks structure only; generated code is never executed here.
func Validate(p string) Validation {
	r := Validation{OK: true, Issues: []string{}, Scope: "structure; generated code was not executed"}
	fail := func(s string) { r.OK = false; r.Issues = append(r.Issues, s) }
	zr, err := zip.OpenReader(p)
	if err != nil {
		fail(err.Error())
		return r
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
		if _, ok := files[f.Name]; ok {
			fail("duplicate archive entry: " + f.Name)
		}
		if f.UncompressedSize64 > 16<<20 || total > 64<<20 {
			fail("archive exceeds size limit")
			return r
		}
		if _, err := safeJoin("root", strings.ReplaceAll(f.Name, string(rune(92)), "/")); err != nil {
			fail(err.Error())
		}
		files[f.Name] = f
	}
	var man Manifest
	if files["manifest.json"] == nil {
		fail("manifest missing")
		return r
	}
	if err := readZipJSON(files["manifest.json"], &man); err != nil {
		fail(err.Error())
		return r
	}
	if man.FormatVersion != FormatVersion || len(man.Items) == 0 {
		fail("invalid manifest version or empty contents")
	}
	seen := map[string]bool{}
	for _, it := range man.Items {
		key := it.Type + ":" + it.Name
		if seen[key] {
			fail("duplicate artifact: " + key)
		}
		seen[key] = true
		f := files[it.Path]
		if f == nil {
			fail("payload missing: " + it.Path)
			continue
		}
		switch it.Type {
		case "workflow":
			var d workflow.Definition
			if err := readZipJSON(f, &d); err != nil {
				fail(err.Error())
			} else if err := ValidateWorkflow(d); err != nil {
				fail(err.Error())
			}
		case "tool":
			var d tools.DynamicToolDef
			if err := readZipJSON(f, &d); err != nil {
				fail(err.Error())
			} else if d.Name != it.Name || !tools.ValidToolName(d.Name) || strings.TrimSpace(d.Code) == "" {
				fail("invalid dynamic tool: " + it.Name)
			} else if err := tools.ParseParametersJSON(d.Parameters); err != nil {
				fail(err.Error())
			}
		case "innerapp":
			var app apps.InnerApp
			if err := readZipJSON(f, &app); err != nil {
				fail(err.Error())
				continue
			}
			if app.Name != it.Name || safeFile(app.Name) != app.Name || app.Name == "" {
				fail("invalid app name")
			}
			if app.Kind != "ui" && app.Kind != "agent" && app.Kind != "workflow" {
				fail("invalid app kind")
			}
			prefix := strings.TrimSuffix(it.Path, "/meta.json") + "/bundle/"
			if app.Kind == "ui" && files[prefix+"index.html"] == nil {
				fail("app index.html missing: " + app.Name)
			}
		case "skill":
			if it.Name == "" || safeFile(it.Name) != it.Name || it.Path != "skills/"+it.Name+"/SKILL.md" {
				fail("invalid skill name")
				continue
			}
			rc, err := f.Open()
			if err != nil {
				fail(err.Error())
				continue
			}
			b, err := io.ReadAll(io.LimitReader(rc, 1<<20))
			_ = rc.Close()
			if err != nil || strings.TrimSpace(string(b)) == "" {
				fail("empty or unreadable skill")
			}
		default:
			fail("unknown artifact type: " + it.Type)
		}
	}
	return r
}
func ValidateWorkflow(d workflow.Definition) error {
	if d.ID == "" || strings.TrimSpace(d.Name) == "" || len(d.Nodes) == 0 {
		return fmt.Errorf("workflow id, name and nodes required")
	}
	nodes := map[string]workflow.Node{}
	start := ""
	ends := 0
	for _, n := range d.Nodes {
		if n.ID == "" || n.Type == "" {
			return fmt.Errorf("node id and type required")
		}
		if _, ok := nodes[n.ID]; ok {
			return fmt.Errorf("duplicate node: %s", n.ID)
		}
		nodes[n.ID] = n
		if n.Type == "start" {
			if start != "" {
				return fmt.Errorf("only one start allowed")
			}
			start = n.ID
		}
		if n.Type == "end" {
			ends++
		}
	}
	if start == "" || ends == 0 {
		return fmt.Errorf("start and end nodes required")
	}
	next := map[string][]string{}
	for _, edge := range d.Edges {
		if _, ok := nodes[edge.From]; !ok {
			return fmt.Errorf("unknown edge source %s", edge.From)
		}
		if _, ok := nodes[edge.To]; !ok {
			return fmt.Errorf("unknown edge target %s", edge.To)
		}
		next[edge.From] = append(next[edge.From], edge.To)
	}
	visited := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		queue = append(queue, next[id]...)
	}
	if len(visited) != len(nodes) {
		return fmt.Errorf("nodes unreachable from start")
	}
	return nil
}
