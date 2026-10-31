package bridge

import (
	"agentgo/internal/agentpack"
	"path/filepath"
	"testing"
)

func TestInitialWorkspaceFallbackIsSeparateFromData(t *testing.T) {
	data := t.TempDir()
	for _, candidate := range []string{"", filepath.Join(data, "missing")} {
		got, err := initialWorkspaceRoot(candidate, data)
		if err != nil {
			t.Fatal(err)
		}
		if !sameWorkspacePath(got, filepath.Join(data, "workspace")) {
			t.Fatalf("fallback exposed data root: %s", got)
		}
	}
	explicit := t.TempDir()
	got, err := initialWorkspaceRoot(explicit, data)
	if err != nil || !sameWorkspacePath(got, explicit) {
		t.Fatalf("valid workspace replaced: %s %v", got, err)
	}
}

func TestWorkspaceSwitchRebindsExtensionEngine(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	r := &Runtime{dataDir: t.TempDir(), agentPack: &agentpack.Engine{Workspace: a}}
	if err := r.applyWorkspaceRoot(b); err != nil {
		t.Fatal(err)
	}
	if r.agentPack.Workspace != b {
		t.Fatal("extension engine kept old workspace")
	}
	if err := r.applyWorkspaceRoot(a); err != nil {
		t.Fatal(err)
	}
	if r.agentPack.Workspace != a {
		t.Fatal("extension engine did not follow rollback")
	}
}
