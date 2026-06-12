package apps

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func testAppStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestScaffoldStatic(t *testing.T) {
	root := t.TempDir()
	store := testAppStore(t)
	sc := NewScaffolder(root, store)
	res, err := sc.Scaffold(context.Background(), ScaffoldOptions{
		Name: "demo_static", Mode: "static", Description: "test",
	})
	if err != nil || !res.Success {
		t.Fatalf("scaffold: res=%+v err=%v", res, err)
	}
	idx, err := os.ReadFile(filepath.Join(root, "demo_static", AppEntryFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(idx), "agentgo-app-helpers.js") {
		t.Fatal("index.html missing helpers")
	}
	app, err := store.GetByName(context.Background(), "demo_static")
	if err != nil || app.Kind != "ui" {
		t.Fatalf("store: %+v err=%v", app, err)
	}
}

func TestUpdateFileValidation(t *testing.T) {
	root := t.TempDir()
	store := testAppStore(t)
	sc := NewScaffolder(root, store)
	_, _ = sc.Scaffold(context.Background(), ScaffoldOptions{Name: "badhtml", Mode: "static"})
	res, _ := sc.UpdateFile(context.Background(), "badhtml", AppEntryFile, "<html><body>no helpers</body></html>")
	if !res.HasCriticalIssues {
		t.Fatal("expected critical validation for missing helpers")
	}
}

func TestAutoFixAndVerify(t *testing.T) {
	root := t.TempDir()
	store := testAppStore(t)
	sc := NewScaffolder(root, store)
	_, _ = sc.Scaffold(context.Background(), ScaffoldOptions{Name: "fixme", Mode: "static"})
	_, _ = sc.UpdateFile(context.Background(), "fixme", AppEntryFile, "<html><body>x</body></html>")
	fixed, err := AutoFixBundle(root, "fixme")
	if err != nil || !fixed {
		t.Fatalf("autofix: fixed=%v err=%v", fixed, err)
	}
	v := VerifyBundle(context.Background(), root, "fixme", nil)
	if v.HasCritical {
		t.Fatalf("expected no critical after fix: %+v", v)
	}
}

func TestScaffoldWorkflowRequiresID(t *testing.T) {
	root := t.TempDir()
	store := testAppStore(t)
	sc := NewScaffolder(root, store)
	res, err := sc.Scaffold(context.Background(), ScaffoldOptions{Name: "wfapp", Mode: "workflow"})
	if err == nil || res.Success {
		t.Fatalf("expected workflow_id error, got %+v err=%v", res, err)
	}
}
