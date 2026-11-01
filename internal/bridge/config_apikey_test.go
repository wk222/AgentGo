package bridge

import "testing"

func TestAPIKeyEnvOverridesStoredKey(t *testing.T) {
	dir := t.TempDir()
	saved := LLMConfig{APIBase: "https://relay.example/v1", APIKey: "sk-stored", Model: "stored/model"}
	if err := saveConfig(dir, saved); err != nil {
		t.Fatal(err)
	}

	// The flag's documented contract: AGENTGO_API_KEY overrides config.json.
	t.Setenv("AGENTGO_API_KEY", "sk-from-env")
	got := loadConfig(dir)
	if got.APIKey != "sk-from-env" {
		t.Fatalf("api_key = %q, want the environment override sk-from-env", got.APIKey)
	}
	if got.APIBase != saved.APIBase || got.Model != saved.Model {
		t.Fatalf("only the key should change, got %+v", got)
	}

	// Unset: the stored key is used again.
	t.Setenv("AGENTGO_API_KEY", "")
	if got := loadConfig(dir); got.APIKey != "sk-stored" {
		t.Fatalf("api_key = %q without the env, want the stored key", got.APIKey)
	}
}
