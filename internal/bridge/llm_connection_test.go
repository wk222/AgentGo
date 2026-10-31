package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestConnectionProbeCallsChatNotOnlyModels(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			t.Error("probe did not test a model inference")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "test-model" || body["stream"] != true || body["max_tokens"] != float64(256) {
			t.Error("probe was not bounded")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "OK"}}}})
	}))
	defer server.Close()
	result := TestLLMConnection(context.Background(), LLMConfig{APIBase: server.URL + "/v1", APIKey: "test-key", Model: "test-model"})
	if !result.OK || hits != 1 {
		t.Fatal(result.Message)
	}
}

func TestConnectionProbeHandlesEmpty401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer server.Close()
	result := TestLLMConnection(context.Background(), LLMConfig{APIBase: server.URL, APIKey: "test-key", Model: "test-model"})
	if result.OK || result.HTTPCode != 401 || result.Message == "" {
		t.Fatal("unauthorized result not reported")
	}
}

func TestConnectionProbeAcceptsSSEBeforeCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Error("duplicated completion path")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := TestLLMConnection(ctx, LLMConfig{APIBase: server.URL + "/v1/chat/completions", APIKey: "test-key", Model: "test-model"})
	if !result.OK {
		t.Fatal(result.Message)
	}
}
