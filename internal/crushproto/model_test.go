package crushproto

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockModelProvider struct {
	mu       sync.Mutex
	model    string
	provider string
}

func (m *mockModelProvider) CurrentModel(workDir string) ModelInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	return ModelInfo{Model: m.model, Provider: m.provider}
}

func (m *mockModelProvider) SetModel(ctx context.Context, workDir, model, provider string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.model = model
	m.provider = provider
	return nil
}

func (m *mockModelProvider) AvailableProviders(workDir string) []any {
	return []any{
		map[string]any{
			"id":                     "ollama",
			"name":                   "Ollama",
			"default_large_model_id": "qwen2.5-coder",
			"models": []any{
				map[string]any{"id": "qwen2.5-coder", "name": "qwen2.5-coder"},
				map[string]any{"id": "deepseek-r1", "name": "deepseek-r1"},
			},
		},
	}
}

// Regression: listWorkspaces once built bodies while holding Server.mu, and
// building a body takes that lock to read the active model -> self-deadlock.
// The handler is driven directly (no httptest.Server) so that, if it ever
// deadlocks again, the test fails fast instead of hanging in server teardown.
func TestListWorkspacesDoesNotDeadlock(t *testing.T) {
	for name, mp := range map[string]ModelProvider{
		"with provider":    &mockModelProvider{model: "m", provider: "ollama"},
		"without provider": nil,
	} {
		t.Run(name, func(t *testing.T) {
			srv, err := New(Options{ModelProvider: mp, Logf: func(string, ...any) {}})
			if err != nil {
				t.Fatal(err)
			}
			defer srv.Close()
			h := srv.Handler()

			create := httptest.NewRecorder()
			h.ServeHTTP(create, httptest.NewRequest("POST", "/v1/workspaces",
				strings.NewReader(`{"path":"`+filepath.ToSlash(t.TempDir())+`","client_id":"c1"}`)))
			if create.Code != 200 {
				t.Fatalf("create workspace = %d %s", create.Code, create.Body)
			}

			done := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/workspaces", nil))
				done <- rec
			}()
			select {
			case rec := <-done:
				var list []map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
					t.Fatalf("list = %d %s (err %v)", rec.Code, rec.Body, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("GET /v1/workspaces hung (lock held while building body)")
			}
		})
	}
}
func TestModelDynamicConfigAndSwitching(t *testing.T) {
	mock := &mockModelProvider{
		model:    "qwen2.5-coder",
		provider: "ollama",
	}

	h := newHarness(t, nil, func(o *Options) {
		o.ModelProvider = mock
	})

	// 1. GET /v1/config returns current active model
	var globalCfg map[string]any
	h.do("GET", "/v1/config", nil, &globalCfg)
	models, _ := globalCfg["models"].(map[string]any)
	large, _ := models["large"].(map[string]any)
	if got := large["model"]; got != "qwen2.5-coder" {
		t.Fatalf("global large model = %v, want qwen2.5-coder", got)
	}

	// 2. GET /v1/workspaces/{id}/config returns current active model
	var wsCfg map[string]any
	h.do("GET", "/v1/workspaces/"+h.ws+"/config", nil, &wsCfg)
	wsModels, _ := wsCfg["models"].(map[string]any)
	wsLarge, _ := wsModels["large"].(map[string]any)
	if got := wsLarge["model"]; got != "qwen2.5-coder" {
		t.Fatalf("workspace large model = %v, want qwen2.5-coder", got)
	}

	// 3. GET /v1/workspaces/{id}/agent returns current active model
	var agentStatus map[string]any
	h.do("GET", "/v1/workspaces/"+h.ws+"/agent", nil, &agentStatus)
	mObj, _ := agentStatus["model"].(map[string]any)
	if got := mObj["id"]; got != "qwen2.5-coder" {
		t.Fatalf("agent model id = %v, want qwen2.5-coder", got)
	}

	// 3b. The workspace body embeds the config the TUI builds its model picker from.
	var wsBody map[string]any
	h.do("GET", "/v1/workspaces/"+h.ws, nil, &wsBody)
	embedded, _ := wsBody["config"].(map[string]any)
	embProviders, _ := embedded["providers"].(map[string]any)
	if _, ok := embProviders["ollama"]; !ok || len(embProviders) != 1 {
		t.Fatalf("embedded config providers = %v, want only ollama", embProviders)
	}

	// 4. GET /v1/workspaces/{id}/providers returns provider list
	var provs []map[string]any
	h.do("GET", "/v1/workspaces/"+h.ws+"/providers", nil, &provs)
	if len(provs) != 1 || provs[0]["id"] != "ollama" {
		t.Fatalf("unexpected providers: %+v", provs)
	}

	// 6. Switch model via POST /v1/workspaces/{id}/config/model
	switchReq := configModelReq{
		Scope:     0,
		ModelType: "large",
	}
	switchReq.Model.Model = "deepseek-r1"
	switchReq.Model.Provider = "ollama"

	code := h.do("POST", "/v1/workspaces/"+h.ws+"/config/model", switchReq, nil)
	if code != http.StatusOK {
		t.Fatalf("POST config/model returned %d", code)
	}

	// 7. Verify mock updated
	mock.mu.Lock()
	updatedModel := mock.model
	mock.mu.Unlock()
	if updatedModel != "deepseek-r1" {
		t.Fatalf("mock model = %s, want deepseek-r1", updatedModel)
	}

	// 8. Verify SSE received config_changed event
	ev, _ := h.until(func(e sseEvent) bool {
		return e.Kind == "config_changed" && e.Op == "updated"
	})
	if ev.Kind != "config_changed" {
		t.Fatalf("expected config_changed SSE event, got: %+v", ev)
	}

	// 9. Re-query agent status: should reflect new model
	h.do("GET", "/v1/workspaces/"+h.ws+"/agent", nil, &agentStatus)
	mObj, _ = agentStatus["model"].(map[string]any)
	if got := mObj["id"]; got != "deepseek-r1" {
		t.Fatalf("agent model id after switch = %v, want deepseek-r1", got)
	}

	// 10. Re-query workspace config: should reflect new model
	h.do("GET", "/v1/workspaces/"+h.ws+"/config", nil, &wsCfg)
	wsModels, _ = wsCfg["models"].(map[string]any)
	wsLarge, _ = wsModels["large"].(map[string]any)
	if got := wsLarge["model"]; got != "deepseek-r1" {
		t.Fatalf("workspace large model after switch = %v, want deepseek-r1", got)
	}
}
