package agentpack

import (
	"agentgo/internal/apps"
	"agentgo/internal/tools"
	"agentgo/internal/workflow"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/cloudwego/eino/components/tool/utils"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillSnapshotRestoreAndAutomaticBackup(t *testing.T) {
	ws := t.TempDir()
	skill := filepath.Join(ws, "skills", "report", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# Report\n\nVersion one"), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(filepath.Dir(skill), "helper.py")
	_ = os.WriteFile(script, []byte("v1"), 0600)
	reloads := 0
	e := &Engine{Workspace: ws, VersionsDir: t.TempDir(), OnReload: func() error { reloads++; return nil }}
	v, err := e.Snapshot(context.Background(), ExportRequest{Title: "First version", Skills: []string{"report"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("# Report\n\nVersion two"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(script, []byte("v2"), 0600)
	res, err := e.Restore(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Installed) != 1 || reloads != 1 {
		t.Fatal("restore did not install and reload skill")
	}
	b, _ := os.ReadFile(skill)
	if !strings.Contains(string(b), "Version one") {
		t.Fatal("version one not restored")
	}
	scriptBody, _ := os.ReadFile(script)
	if string(scriptBody) != "v1" {
		t.Fatal("skill supporting script was not restored")
	}
	versions, err := e.Versions()
	if err != nil || len(versions) != 2 {
		t.Fatal("automatic backup missing", err)
	}
	for _, backup := range versions {
		if backup.ID == v.ID {
			continue
		}
		if _, err := e.Restore(context.Background(), backup.ID); err != nil {
			t.Fatal(err)
		}
		break
	}
	b, _ = os.ReadFile(skill)
	if !strings.Contains(string(b), "Version two") {
		t.Fatal("automatic backup not restorable")
	}
}
func TestSnapshotCannotEscapeWorkspaceOrVersionRoot(t *testing.T) {
	e := &Engine{Workspace: t.TempDir(), VersionsDir: t.TempDir()}
	if _, err := e.Snapshot(context.Background(), ExportRequest{Skills: []string{"../outside"}}); err == nil {
		t.Fatal("skill traversal accepted")
	}
	if _, err := e.Restore(context.Background(), "../../outside"); err == nil {
		t.Fatal("version traversal accepted")
	}
}
func TestValidateWorkflowRejectsDanglingAndUnreachableNodes(t *testing.T) {
	d := workflow.Definition{ID: "custom", Name: "Custom", Nodes: []workflow.Node{{ID: "entry", Type: "start"}, {ID: "exit", Type: "end"}}, Edges: []workflow.Edge{{From: "entry", To: "exit"}}}
	if err := ValidateWorkflow(d); err != nil {
		t.Fatal(err)
	}
	d.Edges[0].To = "missing"
	if err := ValidateWorkflow(d); err == nil {
		t.Fatal("dangling edge accepted")
	}
	d.Edges = nil
	if err := ValidateWorkflow(d); err == nil {
		t.Fatal("unreachable end accepted")
	}
}
func TestInvalidVersionDoesNotMutateLiveSkill(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "skills", "report", "SKILL.md")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	_ = os.WriteFile(path, []byte("live"), 0600)
	e := &Engine{Workspace: ws, VersionsDir: t.TempDir()}
	v, err := e.Snapshot(context.Background(), ExportRequest{Skills: []string{"report"}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := e.versionPath(v.ID)
	_ = os.WriteFile(p, []byte("corrupt"), 0600)
	if _, err := e.Restore(context.Background(), v.ID); err == nil {
		t.Fatal("corrupt version accepted")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "live" {
		t.Fatal("live content was modified")
	}
}

func newVersionEngine(t *testing.T) *Engine {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	wf, err := workflow.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	appStore, err := apps.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	dyn, err := tools.NewDynamicStore(db)
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry()
	reg.SetDynamicSandbox(t.TempDir())
	return &Engine{Workspace: t.TempDir(), VersionsDir: t.TempDir(), AppsRoot: t.TempDir(), WF: wf, Apps: appStore, Dyn: dyn, Reg: reg}
}
func TestAppRestoreRemovesStaleFilesAndCanUndo(t *testing.T) {
	e := newVersionEngine(t)
	ctx := context.Background()
	dir := filepath.Join(e.AppsRoot, "panel")
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>version one</html>"), 0600)
	if err := e.Apps.Upsert(ctx, apps.InnerApp{Name: "panel", Kind: "ui", BundlePath: "panel", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := e.Snapshot(ctx, ExportRequest{InnerApps: []string{"panel"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>version two</html>"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "only-v2.js"), []byte("new"), 0600)
	if _, err := e.Restore(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	if !strings.Contains(string(b), "version one") {
		t.Fatal("old index not restored")
	}
	if _, err := os.Stat(filepath.Join(dir, "only-v2.js")); !os.IsNotExist(err) {
		t.Fatal("stale v2 file still present")
	}
	versions, _ := e.Versions()
	for _, backup := range versions {
		if backup.ID == v.ID {
			continue
		}
		if _, err := e.Restore(ctx, backup.ID); err != nil {
			t.Fatal(err)
		}
		break
	}
	if _, err := os.Stat(filepath.Join(dir, "only-v2.js")); err != nil {
		t.Fatal("automatic backup lost app file")
	}
}
func TestSaveCompleteWorkflowPreservesBranchesAndSnapshotsOldVersion(t *testing.T) {
	e := newVersionEngine(t)
	ctx := context.Background()
	reloads := 0
	e.OnReload = func() error { reloads++; return nil }
	if err := RegisterEvolutionTools(e.Reg, e); err != nil {
		t.Fatal(err)
	}
	def := workflow.Definition{ID: "generated", Name: "Generated", Nodes: []workflow.Node{{ID: "entry", Type: "start"}, {ID: "choose", Type: "branch"}, {ID: "yes", Type: "end"}, {ID: "no", Type: "end"}}, Edges: []workflow.Edge{{From: "entry", To: "choose"}, {From: "choose", To: "yes", When: "true"}, {From: "choose", To: "no", When: "false"}}}
	for i := 0; i < 2; i++ {
		def.Description = fmt.Sprint(i)
		b, _ := json.Marshal(map[string]any{"definition": def})
		out, err := e.Reg.InvokeJSON(ctx, "save_workflow_definition", string(b))
		if err != nil || !strings.Contains(out, "true") {
			t.Fatal(out, err)
		}
	}
	saved, err := e.WF.Get("generated")
	if err != nil || len(saved.Edges) != 3 || saved.Edges[2].When != "false" {
		t.Fatal("branches lost", err)
	}
	versions, _ := e.Versions()
	if len(versions) != 1 || reloads != 2 {
		t.Fatal("pre-edit snapshot/reload missing")
	}
	if _, err := e.Restore(ctx, versions[0].ID); err != nil {
		t.Fatal(err)
	}
	saved, _ = e.WF.Get("generated")
	if saved.Description != "0" {
		t.Fatal("previous workflow not restored")
	}
}
func TestLegacyWorkflowRegistrationUsesActualNodeIDs(t *testing.T) {
	e := newVersionEngine(t)
	d, err := e.WF.SaveFromRegister("Custom", "", "[{\"id\":\"a\",\"type\":\"start\"},{\"id\":\"b\",\"type\":\"end\"}]")
	if err != nil {
		t.Fatal(err)
	}
	if d.ID == "" || len(d.Edges) != 1 || d.Edges[0].From != "a" || d.Edges[0].To != "b" {
		t.Fatal("generated ID/edges invalid", d)
	}
	if _, err := e.WF.SaveFromRegister("Bad", "", "invalid json"); err == nil {
		t.Fatal("malformed generated workflow accepted")
	}
}

func TestGeneratedToolCannotReplaceNativeTool(t *testing.T) {
	e := newVersionEngine(t)
	ctx := context.Background()
	native, err := utils.InferTool("native_test", "native", func(context.Context, struct{}) (string, error) { return "native", nil })
	if err != nil {
		t.Fatal(err)
	}
	e.Reg.AddTool(native)
	def := tools.DynamicToolDef{Name: "native_test", Description: "generated", Code: "print('generated')", Parameters: "{}"}
	if err := e.Reg.RegisterDynamicTool(def); err == nil {
		t.Fatal("native tool replaced")
	}
	if err := e.Dyn.Save(def); err != nil {
		t.Fatal(err)
	}
	if err := e.Reg.SyncDynamicFromStore(e.Dyn); err != nil {
		t.Fatal(err)
	}
	out, err := e.Reg.InvokeJSON(ctx, "native_test", "{}")
	if err != nil || out != "native" {
		t.Fatal("persisted tool shadowed native tool", out, err)
	}
}
