package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LLMConfig mirrors PyBot llm_config (OpenAI-compatible endpoint).
type LLMConfig struct {
	APIBase       string `json:"api_base"`
	APIKey        string `json:"api_key"`
	Model         string `json:"model"`
	FallbackModel string `json:"fallback_model,omitempty"`
}

// GovernanceConfig persists control preset (strict / balanced / open).
type GovernanceConfig struct {
	ControlMode string `json:"control_mode"`
}

// WorkspaceConfig persists the trusted workspace root used by the desktop IDE.
type WorkspaceConfig struct {
	Root string `json:"root,omitempty"`
}

type appConfigFile struct {
	LLM        LLMConfig        `json:"llm"`
	Governance GovernanceConfig `json:"governance"`
	Workspace  WorkspaceConfig  `json:"workspace,omitempty"`
}

// DefaultLLMConfig points at the user's custom relay; override via config.json or env.
func DefaultLLMConfig() LLMConfig {
	return LLMConfig{
		APIBase: "https://api.openai.com/v1",
		APIKey:  os.Getenv("AGENTGO_API_KEY"),
		Model:   envOr("AGENTGO_MODEL", "gpt-4o-mini"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func configPath(dataDir string) string {
	return filepath.Join(dataDir, "config.json")
}

func loadAppConfig(dataDir string) (LLMConfig, GovernanceConfig, WorkspaceConfig) {
	cfg := DefaultLLMConfig()
	gov := GovernanceConfig{ControlMode: "balanced"}
	ws := WorkspaceConfig{}
	b, err := os.ReadFile(configPath(dataDir))
	if err != nil {
		return cfg, gov, ws
	}
	var stored appConfigFile
	if json.Unmarshal(b, &stored) == nil {
		if stored.LLM.APIBase != "" {
			cfg.APIBase = stored.LLM.APIBase
		}
		if stored.LLM.APIKey != "" {
			cfg.APIKey = stored.LLM.APIKey
		}
		if stored.LLM.Model != "" {
			cfg.Model = stored.LLM.Model
		}
		if stored.LLM.FallbackModel != "" {
			cfg.FallbackModel = stored.LLM.FallbackModel
		}
		if stored.Governance.ControlMode != "" {
			gov.ControlMode = stored.Governance.ControlMode
		}
		if stored.Workspace.Root != "" {
			ws.Root = stored.Workspace.Root
		}
	}
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("AGENTGO_API_KEY")
	}
	return cfg, gov, ws
}

func loadConfig(dataDir string) LLMConfig {
	cfg, _, _ := loadAppConfig(dataDir)
	return cfg
}

func saveAppConfig(dataDir string, cfg LLMConfig, gov GovernanceConfig, ws WorkspaceConfig) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	payload := appConfigFile{LLM: cfg, Governance: gov, Workspace: ws}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(dataDir), b, 0o600)
}

func saveConfig(dataDir string, cfg LLMConfig) error {
	_, gov, ws := loadAppConfig(dataDir)
	return saveAppConfig(dataDir, cfg, gov, ws)
}
