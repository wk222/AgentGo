package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePlanFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func listDir(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

// A fork keeps what the runner holds outside the database: the mode it keeps in
// memory, and the plan task list kept as files under the workspace.
func TestAdoptForkedSessionCarriesModeAndPlanTasks(t *testing.T) {
	ctx := context.Background()
	st := openModeTestStore(t)
	root, data := t.TempDir(), t.TempDir()
	r := &Runner{workspaceRoot: root, dataDir: data}
	r.SetSessionsStore(st)
	r.SetSessionMode(SessionMode{Profile: ModeAssistant, Canvas: CanvasFocused})
	if err := st.EnsureSession(ctx, "orig"); err != nil {
		t.Fatal(err)
	}
	r.SetSessionModeForSession("orig", SessionMode{Profile: ModeAdmin, Canvas: CanvasDeep})

	srcDir := planTaskDirectory(WithSessionID(ctx, "orig"), root, data)
	writePlanFile(t, srcDir, "1.json", `{"id":"1","subject":"拆出配置读取","status":"completed","blockedBy":[]}`)
	writePlanFile(t, srcDir, "2.json", `{"id":"2","subject":"补测试","status":"in_progress","blockedBy":["1"]}`)
	writePlanFile(t, srcDir, ".highwatermark", "2")
	writePlanFile(t, srcDir, ".plantask-123456", `{"half":`) // an interrupted write
	writePlanFile(t, srcDir, "nested/9.json", `{"id":"9"}`)  // not part of the list

	res, err := st.ForkSession(ctx, "orig", "")
	if err != nil {
		t.Fatal(err)
	}
	fork := res.Session.ID
	// The row is in the database already, but the runner has not heard of it.
	if got := r.SessionModeForSession(fork); got.Profile != ModeAssistant {
		t.Fatalf("the runner knew the fork's mode before being told: %+v", got)
	}
	adopted, err := r.AdoptForkedSession(ctx, "orig", fork)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.Tasks != 2 {
		t.Fatalf("tasks copied: %+v", adopted)
	}
	if got := r.SessionModeForSession(fork); got.Profile != ModeAdmin || got.Canvas != CanvasDeep {
		t.Fatalf("fork mode: %+v", got)
	}

	dstDir := planTaskDirectory(WithSessionID(ctx, fork), root, data)
	if dstDir == srcDir {
		t.Fatal("both sessions share one task directory")
	}
	if got := listDir(t, dstDir); got != ".highwatermark,1.json,2.json" {
		t.Fatalf("copied files: %s", got)
	}
	// The next task id continues from where the original was, not from 1.
	if b, _ := os.ReadFile(filepath.Join(dstDir, ".highwatermark")); string(b) != "2" {
		t.Fatalf("highwatermark: %q", b)
	}
	tasks, err := loadPlanTaskSnapshot(ctx, dstDir)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("snapshot of the copy: %+v %v", tasks, err)
	}
	// Nothing was left beside the destination.
	if got := listDir(t, filepath.Dir(dstDir)); strings.Contains(got, ".fork-") {
		t.Fatalf("staging directory left behind: %s", got)
	}

	// After the fork each list is its own.
	writePlanFile(t, dstDir, "2.json", `{"id":"2","subject":"只在分叉里改","status":"completed","blockedBy":[]}`)
	if b, _ := os.ReadFile(filepath.Join(srcDir, "2.json")); !strings.Contains(string(b), "补测试") {
		t.Fatalf("the original's task changed: %s", b)
	}
}

func TestAdoptForkedSessionWithoutPlanTasksCreatesNothing(t *testing.T) {
	ctx := context.Background()
	st := openModeTestStore(t)
	root, data := t.TempDir(), t.TempDir()
	r := &Runner{workspaceRoot: root, dataDir: data}
	r.SetSessionsStore(st)
	if err := st.EnsureSession(ctx, "orig"); err != nil {
		t.Fatal(err)
	}
	res, err := st.ForkSession(ctx, "orig", "")
	if err != nil {
		t.Fatal(err)
	}
	adopted, err := r.AdoptForkedSession(ctx, "orig", res.Session.ID)
	if err != nil || adopted.Tasks != 0 {
		t.Fatalf("%+v %v", adopted, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".tasks")); !os.IsNotExist(err) {
		t.Fatalf("a task directory appeared for a session that had no plan: %v", err)
	}
	// An empty directory the original left behind copies as nothing.
	srcDir := planTaskDirectory(WithSessionID(ctx, "orig"), root, data)
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ = st.ForkSession(ctx, "orig", "")
	if adopted, err = r.AdoptForkedSession(ctx, "orig", res.Session.ID); err != nil || adopted.Tasks != 0 {
		t.Fatalf("empty dir: %+v %v", adopted, err)
	}
	if _, err := os.Stat(planTaskDirectory(WithSessionID(ctx, res.Session.ID), root, data)); !os.IsNotExist(err) {
		t.Fatal("an empty list was copied as a directory")
	}
}

func TestAdoptForkedSessionRefusesWhatItCannotCopyCleanly(t *testing.T) {
	ctx := context.Background()
	st := openModeTestStore(t)
	root, data := t.TempDir(), t.TempDir()
	r := &Runner{workspaceRoot: root, dataDir: data}
	r.SetSessionsStore(st)

	if _, err := r.AdoptForkedSession(ctx, "same", "same"); err == nil {
		t.Fatal("a session cannot be forked onto itself")
	}
	if _, err := r.AdoptForkedSession(ctx, "", "x"); err == nil {
		t.Fatal("empty id accepted")
	}
	var nilRunner *Runner
	if _, err := nilRunner.AdoptForkedSession(ctx, "a", "b"); err != nil {
		t.Fatalf("nil runner: %v", err)
	}

	srcDir := planTaskDirectory(WithSessionID(ctx, "orig"), root, data)
	writePlanFile(t, srcDir, "1.json", `{"id":"1"}`)
	// A task record that is not a task record at all.
	writePlanFile(t, srcDir, "2.json", strings.Repeat("x", forkMaxTaskBytes+1))
	dstDir := planTaskDirectory(WithSessionID(ctx, "fork"), root, data)
	if _, err := r.AdoptForkedSession(ctx, "orig", "fork"); err == nil {
		t.Fatal("an oversized record was copied")
	}
	if _, err := os.Stat(dstDir); !os.IsNotExist(err) {
		t.Fatalf("a failed copy left a partial task list: %v", err)
	}
	if got := listDir(t, filepath.Dir(dstDir)); strings.Contains(got, ".fork-") {
		t.Fatalf("staging directory left behind: %s", got)
	}

	// A destination that already has tasks is not overwritten.
	if err := os.Remove(filepath.Join(srcDir, "2.json")); err != nil {
		t.Fatal(err)
	}
	writePlanFile(t, dstDir, "7.json", `{"id":"7"}`)
	if _, err := r.AdoptForkedSession(ctx, "orig", "fork"); err == nil {
		t.Fatal("existing tasks were overwritten")
	}
	if got := listDir(t, dstDir); got != "7.json" {
		t.Fatalf("destination touched: %s", got)
	}
}
