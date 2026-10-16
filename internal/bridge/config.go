package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"agentgo/internal/config"
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
	apiBase := "https://api.openai.com/v1"
	apiKey := config.APIKey.Get()
	model := config.Model.Get()

	if apiKey == "" {
		if azKey := os.Getenv("AZURE_OPENAI_API_KEY"); azKey != "" {
			if azEp := os.Getenv("AZURE_OPENAI_API_ENDPOINT"); azEp != "" {
				apiKey = azKey
				apiBase = strings.TrimRight(azEp, "/") + "/openai/v1/"
				model = azureDeployment()
			}
		}
	}

	return LLMConfig{
		APIBase: apiBase,
		APIKey:  apiKey,
		Model:   model,
	}
}

// azureDeployment is the Azure deployment name, defaulting to the Luna deployment.
func azureDeployment() string {
	if v := config.AzureDeployment.Get(); v != "" {
		return v
	}
	return "gpt-6-luna"
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
		if k := config.APIKey.Get(); k != "" {
			cfg.APIKey = k
		} else if azKey := os.Getenv("AZURE_OPENAI_API_KEY"); azKey != "" {
			if azEp := os.Getenv("AZURE_OPENAI_API_ENDPOINT"); azEp != "" {
				cfg.APIKey = azKey
				if cfg.APIBase == "" || cfg.APIBase == "https://api.openai.com/v1" {
					cfg.APIBase = strings.TrimRight(azEp, "/") + "/openai/v1/"
				}
				if cfg.Model == "" || cfg.Model == "gpt-4o-mini" {
					cfg.Model = azureDeployment()
				}
			}
		}
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
