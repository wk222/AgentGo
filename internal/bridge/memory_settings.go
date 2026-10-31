package bridge

import (
	"agentgo/internal/config"
	"agentgo/internal/memory"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type MemorySettings struct {
	TotalTokens     int  `json:"total_tokens"`
	AutomaticTokens int  `json:"automatic_tokens"`
	Hierarchy       bool `json:"hierarchy"`
}

func currentMemorySettings() MemorySettings {
	return MemorySettings{memory.TotalTokenBudget(), memory.AutomaticTokenBudget(), config.MemoryHierarchy.On()}
}
func validateMemorySettings(c MemorySettings) error {
	if c.TotalTokens < 0 || c.TotalTokens > 32768 || c.AutomaticTokens < 0 || c.AutomaticTokens > c.TotalTokens {
		return fmt.Errorf("total tokens must be 0–32768; automatic tokens must be between 0 and total")
	}
	return nil
}
func applyMemorySettings(c MemorySettings) {
	config.MemoryTokens.Set(c.TotalTokens)
	config.MemoryAutoTokens.Set(c.AutomaticTokens)
	config.MemoryHierarchy.Set(c.Hierarchy)
}
func loadMemorySettings(dataDir string) error {
	raw, err := os.ReadFile(filepath.Join(dataDir, "memory-settings.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var c MemorySettings
	if err = json.Unmarshal(raw, &c); err != nil {
		return err
	}
	if err = validateMemorySettings(c); err != nil {
		return err
	}
	// Explicit launcher settings take precedence over saved desktop preferences.
	config.MemoryTokens.SetDefault(c.TotalTokens)
	config.MemoryAutoTokens.SetDefault(c.AutomaticTokens)
	config.MemoryHierarchy.SetDefault(c.Hierarchy)
	return nil
}
func saveMemorySettings(dataDir string, c MemorySettings) error {
	if err := validateMemorySettings(c); err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dataDir, ".memory-settings-*.tmp")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	raw, err := json.MarshalIndent(c, "", "  ")
	if err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, filepath.Join(dataDir, "memory-settings.json"))
}
func (s *AppService) GetMemorySettings() MemorySettings { return currentMemorySettings() }
func (s *AppService) SetMemorySettings(totalTokens, automaticTokens int, hierarchy bool) map[string]any {
	c := MemorySettings{totalTokens, automaticTokens, hierarchy}
	if err := validateMemorySettings(c); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if s == nil || s.rt == nil {
		return map[string]any{"success": false, "error": "runtime unavailable"}
	}
	s.rt.mu.Lock()
	defer s.rt.mu.Unlock()
	if err := saveMemorySettings(s.rt.dataDir, c); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	applyMemorySettings(c)
	return map[string]any{"success": true, "settings": c}
}
