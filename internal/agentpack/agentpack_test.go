package agentpack

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasExecutableItemsTreatsToolTypeAsExecutable(t *testing.T) {
	if !hasExecutableItems([]ManifestItem{{Type: "tool", Name: "demo", Executable: false}}) {
		t.Fatal("tool items must be treated as executable even when the manifest flag is false")
	}
	if !hasExecutableItems([]ManifestItem{{Type: "workflow", Name: "demo", Executable: true}}) {
		t.Fatal("explicit executable flag should still be honored")
	}
	if hasExecutableItems([]ManifestItem{{Type: "workflow", Name: "demo"}}) {
		t.Fatal("plain workflows should not be treated as executable")
	}
}

func TestImportRequiresConfirmForToolTypeEvenWithoutExecutableFlag(t *testing.T) {
	packPath := writeAgentPack(t, map[string]any{
		"manifest.json": Manifest{
			FormatVersion: FormatVersion,
			Items: []ManifestItem{{
				Type:       "tool",
				Name:       "demo_tool",
				Path:       "tools/demo_tool.json",
				Executable: false,
			}},
		},
		"tools/demo_tool.json": map[string]any{"name": "demo_tool"},
	})

	res, err := (&Engine{}).Import(context.Background(), packPath, false, false)
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if !res.HasExecutable || !res.NeedConfirm {
		t.Fatalf("expected executable confirm gate, got has=%v need=%v", res.HasExecutable, res.NeedConfirm)
	}
	if len(res.Installed) != 0 {
		t.Fatalf("expected no installed items before confirm, got %d", len(res.Installed))
	}
}

func TestImportRejectsInnerAppZipSlip(t *testing.T) {
	packPath := writeAgentPack(t, map[string]any{
		"manifest.json": Manifest{
			FormatVersion: FormatVersion,
			Items: []ManifestItem{{
				Type: "innerapp",
				Name: "demo_app",
				Path: "apps/demo_app/meta.json",
			}},
		},
		"apps/demo_app/meta.json":          map[string]any{"name": "demo_app", "kind": "ui", "enabled": true},
		"apps/demo_app/bundle/../evil.txt": map[string]any{"oops": true},
	})

	res, err := (&Engine{AppsRoot: t.TempDir()}).Import(context.Background(), packPath, true, true)
	if err != nil {
		t.Fatalf("Import returned error: %v", err)
	}
	if len(res.Installed) != 0 {
		t.Fatalf("expected no installed items, got %d", len(res.Installed))
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0].Reason, "illegal path") {
		t.Fatalf("expected illegal path skip, got %#v", res.Skipped)
	}
}

func TestExtractZipFileRefusesSymlinkDestination(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside target: %v", err)
	}
	dest := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, dest); err != nil {
		t.Skipf("symlink unavailable on this platform or user: %v", err)
	}

	zipPath := filepath.Join(t.TempDir(), "payload.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("payload.txt")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write([]byte("payload")); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close zip file: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()
	if len(zr.File) != 1 {
		t.Fatalf("expected one zip file, got %d", len(zr.File))
	}

	err = extractZipFile(zr.File[0], root, dest)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink refusal, got %v", err)
	}
}

func writeAgentPack(t *testing.T, entries map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.agentpack")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create pack: %v", err)
	}
	zw := zip.NewWriter(f)
	for name, v := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close pack: %v", err)
	}
	return path
}
