package bridge

import (
	"agentgo/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestMemorySettingsValidateAndPersistWithoutTouchingLLMCredentials(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configFile, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	want := MemorySettings{5000, 900, true}
	if err := saveMemorySettings(dir, want); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []MemorySettings{{-1, 0, true}, {100, 101, true}, {32769, 0, true}} {
		if err := saveMemorySettings(dir, bad); err == nil {
			t.Fatal("invalid accepted", bad)
		}
	}
	t.Setenv(config.MemoryTokens.Name(), "")
	t.Setenv(config.MemoryAutoTokens.Name(), "")
	t.Setenv(config.MemoryHierarchy.Name(), "")
	if err := loadMemorySettings(dir); err != nil {
		t.Fatal(err)
	}
	if got := currentMemorySettings(); got != want {
		t.Fatal(got)
	}
	raw, err := os.ReadFile(configFile)
	if err != nil || string(raw) != "untouched" {
		t.Fatal("overwrote main config", err)
	}
	// A zero budget disables reads without changing persistence.
	if err = saveMemorySettings(dir, MemorySettings{0, 0, true}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.MemoryTokens.Name(), "2000")
	if err = loadMemorySettings(dir); err != nil || config.MemoryTokens.Get() != 2000 {
		t.Fatal("launcher override lost", err)
	}
}
