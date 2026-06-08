package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxWorkspaceLifecycle(t *testing.T) {
	// 1. Create a dummy real repository
	realRepo, err := os.MkdirTemp("", "real-repo-*")
	if err != nil {
		t.Fatalf("failed creating temp real repo: %v", err)
	}
	defer os.RemoveAll(realRepo)

	// Create SOUL.md
	soulPath := filepath.Join(realRepo, "SOUL.md")
	if err := os.WriteFile(soulPath, []byte("Identity: AgentGo"), 0644); err != nil {
		t.Fatalf("failed writing SOUL.md: %v", err)
	}

	// Create node_modules and dummy file inside it
	nodeModulesDir := filepath.Join(realRepo, "node_modules")
	if err := os.MkdirAll(nodeModulesDir, 0755); err != nil {
		t.Fatalf("failed creating node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nodeModulesDir, "dep.txt"), []byte("dependency"), 0644); err != nil {
		t.Fatalf("failed writing dep.txt: %v", err)
	}

	// 2. Clone it to Sandbox
	sandboxDir, err := CreateSandboxWorkspace(realRepo)
	if err != nil {
		t.Fatalf("failed creating sandbox workspace: %v", err)
	}
	defer CleanupSandboxWorkspace(sandboxDir)

	// Verify SOUL.md exists in sandbox
	sandboxSoul := filepath.Join(sandboxDir, "SOUL.md")
	if _, err := os.Stat(sandboxSoul); os.IsNotExist(err) {
		t.Errorf("SOUL.md was not copied to sandbox")
	}

	// Verify node_modules does not exist in sandbox
	sandboxNodeModules := filepath.Join(sandboxDir, "node_modules")
	if _, err := os.Stat(sandboxNodeModules); !os.IsNotExist(err) {
		t.Errorf("node_modules should have been excluded from sandbox clone")
	}

	// 3. Simulate build output inside sandbox (e.g. dist folder)
	sandboxDist := filepath.Join(sandboxDir, "dist")
	if err := os.MkdirAll(sandboxDist, 0755); err != nil {
		t.Fatalf("failed creating sandbox dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sandboxDist, "index.html"), []byte("<html></html>"), 0644); err != nil {
		t.Fatalf("failed creating index.html in sandbox dist: %v", err)
	}

	// 4. Sync dist back to real repo
	if err := SyncSandboxToReal(sandboxDir, realRepo, "dist"); err != nil {
		t.Fatalf("failed syncing sandbox back to real repository: %v", err)
	}

	// Verify real repo now has dist/index.html
	realIndex := filepath.Join(realRepo, "dist", "index.html")
	if _, err := os.Stat(realIndex); os.IsNotExist(err) {
		t.Errorf("index.html was not synchronized back to the real repository")
	}

	// 5. Test cleanup boundary check
	errCleanup := CleanupSandboxWorkspace("/invalid/path")
	if errCleanup == nil || !strings.Contains(errCleanup.Error(), "denied cleanup") {
		t.Errorf("expected boundary check error when cleaning up invalid path, got: %v", errCleanup)
	}
}
