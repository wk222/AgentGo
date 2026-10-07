package bridge

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// CrushHandler is the brain half of the embedded terminal: it must serve the
// protocol with no TCP port (AGENTGO_CRUSHPROTO_ADDR unset) and stay quiet.
func TestCrushHandlerServesProtocolWithoutPort(t *testing.T) {
	t.Setenv("AGENTGO_CRUSHPROTO_ADDR", "")
	rt := &Runtime{dataDir: t.TempDir(), llm: LLMConfig{APIBase: "http://127.0.0.1:11434/v1", Model: "qwen3"}}
	svc := NewAppService(rt)
	defer svc.Close()

	h, closeBrain, err := svc.CrushHandler()
	if err != nil {
		t.Fatal(err)
	}
	defer closeBrain()

	do := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		var rd bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&rd).Encode(body)
		}
		req := httptest.NewRequest(method, "/v1"+path, &rd)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := do(http.MethodGet, "/health", nil); rec.Code != http.StatusOK {
		t.Fatalf("health = %d", rec.Code)
	}

	ws := t.TempDir()
	rec := do(http.MethodPost, "/workspaces", map[string]any{
		"path": ws, "data_dir": t.TempDir(), "client_id": "33333333-3333-4333-8333-333333333333", "env": []string{},
	})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("create workspace = %d: %s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created["id"] == "" || created["id"] == nil {
		t.Fatalf("no workspace id: %v / %s", err, rec.Body.String())
	}

	// The model overlay must reach the embedded config the TUI reads.
	cfg, _ := created["config"].(map[string]any)
	if cfg == nil {
		t.Fatalf("workspace has no embedded config: %s", rec.Body.String())
	}
	models, _ := json.Marshal(cfg["models"])
	if !bytes.Contains(models, []byte("qwen3")) {
		t.Fatalf("embedded config does not expose the configured model: %s", models)
	}

	if rec := do(http.MethodGet, "/workspaces", nil); rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
}

func TestCrushHandlerRejectsNilService(t *testing.T) {
	var s *AppService
	if _, _, err := s.CrushHandler(); err == nil {
		t.Fatal("expected error for nil service")
	}
}
