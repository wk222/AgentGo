package bridge

import (
	"context"
	"fmt"
	"strings"

	"agentgo/internal/crushproto"
)

// crushprotoModelBridge exposes AgentGo's single configured LLM endpoint to the
// Crush TUI as one provider whose models are the primary + fallback model.
// Models of other vendors are deliberately NOT offered: AgentGo could not run
// them against this endpoint.
type crushprotoModelBridge struct {
	svc *AppService
}

var _ crushproto.ModelProvider = (*crushprotoModelBridge)(nil)

func inferProvider(apiBase string) string {
	lower := strings.ToLower(apiBase)
	switch {
	case strings.Contains(lower, "azure"):
		return "azure"
	case strings.Contains(lower, "anthropic"):
		return "anthropic"
	case strings.Contains(lower, "11434") || strings.Contains(lower, "ollama"):
		return "ollama"
	case strings.Contains(lower, "openrouter"):
		return "openrouter"
	case strings.Contains(lower, "deepseek"):
		return "deepseek"
	case strings.Contains(lower, "bigmodel"):
		return "glm"
	case strings.Contains(lower, "dashscope"):
		return "qwen"
	default:
		return "openai"
	}
}

func (b *crushprotoModelBridge) CurrentModel(string) crushproto.ModelInfo {
	cfg := b.svc.rt.LLMConfig()
	model := cfg.Model
	if model == "" {
		model = "gpt-4o-mini"
	}
	return crushproto.ModelInfo{Model: model, Provider: inferProvider(cfg.APIBase)}
}

// SetModel switches the primary model. Only the configured provider's primary
// or fallback model is accepted; choosing the fallback swaps the two so the
// previous model stays selectable.
func (b *crushprotoModelBridge) SetModel(_ context.Context, _ string, model, provider string) error {
	cfg := b.svc.rt.LLMConfig()
	if want := inferProvider(cfg.APIBase); provider != "" && provider != want {
		return fmt.Errorf("provider %q is not configured in AgentGo (configured: %q)", provider, want)
	}
	switch {
	case model == cfg.Model:
		return nil
	case cfg.FallbackModel != "" && model == cfg.FallbackModel:
		cfg.FallbackModel, cfg.Model = cfg.Model, model
	default:
		return fmt.Errorf("model %q is not available (available: %s)", model, strings.Join(configuredModels(cfg), ", "))
	}
	return b.svc.rt.SetLLMConfig(cfg)
}

func configuredModels(cfg LLMConfig) []string {
	out := []string{cfg.Model}
	if cfg.FallbackModel != "" && cfg.FallbackModel != cfg.Model {
		out = append(out, cfg.FallbackModel)
	}
	return out
}

func (b *crushprotoModelBridge) AvailableProviders(string) []any {
	cfg := b.svc.rt.LLMConfig()
	id := inferProvider(cfg.APIBase)
	models := make([]any, 0, 2)
	for _, m := range configuredModels(cfg) {
		if m == "" {
			continue
		}
		models = append(models, map[string]any{
			"id": m, "name": m,
			"cost_per_1m_in": 0, "cost_per_1m_out": 0,
			"cost_per_1m_in_cached": 0, "cost_per_1m_out_cached": 0,
			"context_window": 128000, "default_max_tokens": 16384,
			"can_reason": false, "supports_attachments": true,
		})
	}
	return []any{map[string]any{
		"id": id, "name": "AgentGo (" + id + ")", "type": "openai-compat",
		"base_url": cfg.APIBase, "api_key": "",
		"default_large_model_id": cfg.Model, "default_small_model_id": cfg.Model,
		"models": models,
	}}
}
