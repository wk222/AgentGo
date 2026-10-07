package bridge

import (
	"context"
	"testing"
)

func newModelBridge(t *testing.T, cfg LLMConfig) *crushprotoModelBridge {
	t.Helper()
	rt := &Runtime{dataDir: t.TempDir(), llm: cfg}
	return &crushprotoModelBridge{svc: NewAppService(rt)}
}

func TestModelBridgeOffersOnlyConfiguredModels(t *testing.T) {
	b := newModelBridge(t, LLMConfig{APIBase: "http://127.0.0.1:11434/v1", Model: "qwen3", FallbackModel: "llama3"})
	list := b.AvailableProviders("")
	if len(list) != 1 {
		t.Fatalf("providers = %d, want 1 (no foreign vendors)", len(list))
	}
	p := list[0].(map[string]any)
	if p["id"] != "ollama" {
		t.Fatalf("provider id = %v", p["id"])
	}
	if got := len(p["models"].([]any)); got != 2 {
		t.Fatalf("models = %d, want primary+fallback", got)
	}
}

func TestModelBridgeSwitchSwapsFallbackAndRejectsForeign(t *testing.T) {
	b := newModelBridge(t, LLMConfig{APIBase: "https://api.openai.com/v1", Model: "gpt-4o", FallbackModel: "gpt-4o-mini"})
	ctx := context.Background()

	if err := b.SetModel(ctx, "", "claude-opus", "openai"); err == nil {
		t.Fatal("unknown model accepted")
	}
	if err := b.SetModel(ctx, "", "gpt-4o", "anthropic"); err == nil {
		t.Fatal("foreign provider accepted")
	}
	if err := b.SetModel(ctx, "", "gpt-4o-mini", "openai"); err != nil {
		t.Fatal(err)
	}
	cfg := b.svc.rt.LLMConfig()
	if cfg.Model != "gpt-4o-mini" || cfg.FallbackModel != "gpt-4o" {
		t.Fatalf("after switch: model=%q fallback=%q", cfg.Model, cfg.FallbackModel)
	}
	if got := b.CurrentModel("").Model; got != "gpt-4o-mini" {
		t.Fatalf("CurrentModel = %q", got)
	}
}
