package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestLangfuseExporter(t *testing.T) {
	var receivedPayload IngestionPayload

	// 1. Setup mock http server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "pk-test" || password != "sk-test" {
			t.Errorf("basic auth failed: got user=%q, pass=%q", username, password)
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed reading body: %v", err)
		}
		_ = json.Unmarshal(bodyBytes, &receivedPayload)
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	// 2. Setup exporter and OTel pipeline
	exporter := NewLangfuseExporter("pk-test", "sk-test", server.URL)
	ssp := sdktrace.NewSimpleSpanProcessor(exporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(ssp),
	)
	defer tp.Shutdown(context.Background())

	tracer := tp.Tracer("agentgo-telemetry-test")

	// 3. Start span mimicking Eino LLM orchestration
	_, span := tracer.Start(context.Background(), "chat-completion-span")
	span.SetAttributes(
		attribute.String("model", "gpt-4"),
		attribute.String("prompt", "hello agentgo"),
		attribute.String("completion", "hi there"),
		attribute.Int64("prompt_tokens", 10),
		attribute.Int64("completion_tokens", 15),
		attribute.String("session_id", "session-uuid-999"),
	)
	span.End()

	// Flush spans
	_ = tp.ForceFlush(context.Background())

	// 4. Validate output mappings
	if len(receivedPayload.Batch) == 0 {
		t.Fatalf("no ingestion events received at langfuse mock server")
	}

	var hasTrace, hasGeneration bool
	for _, ev := range receivedPayload.Batch {
		if ev.Type == "trace-create" {
			hasTrace = true
			body, ok := ev.Body.(map[string]any)
			if !ok {
				t.Errorf("trace-create body invalid type")
			} else if body["sessionId"] != "session-uuid-999" {
				t.Errorf("trace-create sessionId mismatch: got %v", body["sessionId"])
			}
		}
		if ev.Type == "generation-create" {
			hasGeneration = true
			body, ok := ev.Body.(map[string]any)
			if !ok {
				t.Errorf("generation-create body invalid type")
			} else if body["model"] != "gpt-4" || body["input"] != "hello agentgo" {
				t.Errorf("generation-create body values mismatch: got model=%v, input=%v", body["model"], body["input"])
			}
		}
	}

	if !hasTrace {
		t.Errorf("missing trace-create event")
	}
	if !hasGeneration {
		t.Errorf("missing generation-create event")
	}
}
