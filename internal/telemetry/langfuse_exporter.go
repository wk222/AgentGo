package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

// Langfuse Ingestion structures
type IngestionEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // trace-create, span-create, generation-create
	Timestamp time.Time `json:"timestamp"`
	Body      any       `json:"body"`
}

type IngestionPayload struct {
	Batch []IngestionEvent `json:"batch"`
}

type TraceBody struct {
	ID        string    `json:"id"`
	Name      string    `json:"name,omitempty"`
	UserID    string    `json:"userId,omitempty"`
	SessionID string    `json:"sessionId,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

type SpanBody struct {
	ID        string    `json:"id"`
	TraceID   string    `json:"traceId"`
	ParentID  string    `json:"parentObserveId,omitempty"`
	Name      string    `json:"name"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime,omitempty"`
	Input     any       `json:"input,omitempty"`
	Output    any       `json:"output,omitempty"`
	Metadata  any       `json:"metadata,omitempty"`
}

type GenerationBody struct {
	ID              string    `json:"id"`
	TraceID         string    `json:"traceId"`
	ParentID        string    `json:"parentObserveId,omitempty"`
	Name            string    `json:"name"`
	StartTime       time.Time `json:"startTime"`
	EndTime         time.Time `json:"endTime,omitempty"`
	Model           string    `json:"model,omitempty"`
	ModelParameters any       `json:"modelParameters,omitempty"`
	Input           any       `json:"input,omitempty"`
	Output          any       `json:"output,omitempty"`
	Usage           *Usage    `json:"usage,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"input"`
	CompletionTokens int `json:"output"`
	TotalTokens      int `json:"total"`
}

// LangfuseExporter implements trace.SpanExporter
type LangfuseExporter struct {
	publicKey  string
	secretKey  string
	host       string
	httpClient *http.Client
}

// NewLangfuseExporter creates a new exporter instance.
func NewLangfuseExporter(publicKey, secretKey, host string) *LangfuseExporter {
	if host == "" {
		host = "https://cloud.langfuse.com"
	}
	host = strings.TrimSuffix(host, "/")
	return &LangfuseExporter{
		publicKey:  publicKey,
		secretKey:  secretKey,
		host:       host,
		httpClient: &http.Client{Timeout: 8 * time.Second},
	}
}

// ExportSpans converts ReadOnlySpans to Langfuse structures and uploads them.
func (e *LangfuseExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	var batch []IngestionEvent

	for _, span := range spans {
		traceID := span.SpanContext().TraceID().String()
		spanID := span.SpanContext().SpanID().String()
		parentID := span.Parent().SpanID().String()
		if parentID == "0000000000000000" {
			parentID = ""
		}

		// Check if it is a Root Span
		isRoot := span.Parent().SpanID().String() == "0000000000000000"

		// Extract attributes
		var modelName string
		var prompt, completion string
		var promptTokens, completionTokens int
		var hasLLMAttribute bool
		var sessionID, userID string

		for _, kv := range span.Attributes() {
			switch kv.Key {
			case "gen_ai.request.model", "model":
				modelName = kv.Value.AsString()
				hasLLMAttribute = true
			case "gen_ai.prompt", "prompt", "input":
				prompt = kv.Value.AsString()
				hasLLMAttribute = true
			case "gen_ai.completion", "completion", "output":
				completion = kv.Value.AsString()
				hasLLMAttribute = true
			case "gen_ai.usage.prompt_tokens", "prompt_tokens":
				promptTokens = int(kv.Value.AsInt64())
				hasLLMAttribute = true
			case "gen_ai.usage.completion_tokens", "completion_tokens":
				completionTokens = int(kv.Value.AsInt64())
				hasLLMAttribute = true
			case "session_id", "session.id":
				sessionID = kv.Value.AsString()
			case "user_id", "user.id":
				userID = kv.Value.AsString()
			}
		}

		// 1. If Root Span, emit trace-create event
		if isRoot {
			batch = append(batch, IngestionEvent{
				ID:        traceID,
				Type:      "trace-create",
				Timestamp: span.StartTime(),
				Body: TraceBody{
					ID:        traceID,
					Name:      span.Name(),
					UserID:    userID,
					SessionID: sessionID,
					Timestamp: span.StartTime(),
				},
			})
		}

		// 2. Classify generation vs standard span
		isGeneration := hasLLMAttribute || strings.Contains(strings.ToLower(span.Name()), "chat") || strings.Contains(strings.ToLower(span.Name()), "llm")
		if isGeneration {
			usageVal := &Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			}
			if usageVal.TotalTokens == 0 {
				usageVal = nil
			}

			batch = append(batch, IngestionEvent{
				ID:        spanID,
				Type:      "generation-create",
				Timestamp: span.StartTime(),
				Body: GenerationBody{
					ID:        spanID,
					TraceID:   traceID,
					ParentID:  parentID,
					Name:      span.Name(),
					StartTime: span.StartTime(),
					EndTime:   span.EndTime(),
					Model:     modelName,
					Input:     prompt,
					Output:    completion,
					Usage:     usageVal,
				},
			})
		} else {
			batch = append(batch, IngestionEvent{
				ID:        spanID,
				Type:      "span-create",
				Timestamp: span.StartTime(),
				Body: SpanBody{
					ID:        spanID,
					TraceID:   traceID,
					ParentID:  parentID,
					Name:      span.Name(),
					StartTime: span.StartTime(),
					EndTime:   span.EndTime(),
					Input:     prompt,
					Output:    completion,
				},
			})
		}
	}

	if len(batch) == 0 {
		return nil
	}

	return e.upload(ctx, batch)
}

func (e *LangfuseExporter) upload(ctx context.Context, batch []IngestionEvent) error {
	payload := IngestionPayload{Batch: batch}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/public/ingestion", e.host)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(e.publicKey, e.secretKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMultiStatus {
		return fmt.Errorf("langfuse upload status failed: %s", resp.Status)
	}
	return nil
}

// Shutdown shuts down the exporter connection.
func (e *LangfuseExporter) Shutdown(ctx context.Context) error {
	return nil
}

// InitializeOTel registers a global tracer provider mapped to Langfuse exporter and returns the shutdown function.
func InitializeOTel(publicKey, secretKey, host string) (func(), error) {
	exporter := NewLangfuseExporter(publicKey, secretKey, host)

	// Create resource definitions
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("agentgo-service"),
		),
	)
	if err != nil {
		return nil, err
	}

	bsp := sdktrace.NewBatchSpanProcessor(exporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(bsp),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)

	shutdown := func() {
		_ = tp.Shutdown(context.Background())
	}
	return shutdown, nil
}
