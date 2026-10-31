package agent

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestPromptCalibrationAndEstimation(t *testing.T) {
	ResetPromptCalibrationForTest()
	defer ResetPromptCalibrationForTest()

	// Below 1000 prompt tokens: ignored (too noisy)
	RecordOutboundRequestBytes("example/calibration-model", 2000)
	RecordPromptCalibration("example/calibration-model", 0, 500)
	if _, ok := CalibratedBytesPerToken("example/calibration-model"); ok {
		t.Fatal("expected calibration below 1000 tokens to be ignored")
	}

	// 4000 bytes -> 2000 tokens => ratio = 2.0 bytes/token
	RecordOutboundRequestBytes("example/calibration-model", 4000)
	RecordPromptCalibration("example/calibration-model", 0, 2000)
	r, ok := CalibratedBytesPerToken("example/calibration-model")
	if !ok || r != 2.0 {
		t.Fatalf("expected ratio 2.0, got %v (ok=%v)", r, ok)
	}

	// Conservative minimum: higher ratio (3.0) does not overwrite 2.0; lower ratio (1.5) updates it
	RecordPromptCalibration("example/calibration-model", 6000, 2000)
	if r, _ = CalibratedBytesPerToken("example/calibration-model"); r != 2.0 {
		t.Fatalf("expected conservative min ratio 2.0 to be preserved, got %v", r)
	}
	RecordPromptCalibration("example/calibration-model", 3000, 2000)
	if r, _ = CalibratedBytesPerToken("example/calibration-model"); r != 1.5 {
		t.Fatalf("expected conservative min ratio 1.5, got %v", r)
	}

	sample := strings.Repeat("a", 3000)
	est := EstimateCalibratedTokens("example/calibration-model", sample)
	if est < 2000 {
		t.Fatalf("expected calibrated estimate >= 2000 for 3000 bytes at ratio 1.5, got %d", est)
	}
}

func TestResilienceTransport_PreOutputSSE507Retry(t *testing.T) {
	ResetPromptCalibrationForTest()
	defer ResetPromptCalibrationForTest()

	var attempts atomic.Int32
	tr := &llmResilienceTransport{
		streamIdleTimeout: 2 * time.Second,
		max507Retries:     3,
		retry507Backoff:   5 * time.Millisecond,
		next: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			n := attempts.Add(1)
			h := make(http.Header)
			h.Set("Content-Type", "text/event-stream")
			if n <= 2 {
				// First two attempts emit an SSE heartbeat followed by MoMA error.code=507 before any business delta
				sse507 := ": heartbeat\n\ndata: {\"model\":\"glm-5.2-w4a8\",\"error\":{\"code\":507,\"message\":\"overloaded\"}}\n\n"
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     h,
					Body:       io.NopCloser(strings.NewReader(sse507)),
				}, nil
			}
			// Third attempt succeeds with real business output
			sseOK := "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
				"data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"hello intranet\"}}]}\n\n" +
				"data: [DONE]\n\n"
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     h,
				Body:       io.NopCloser(strings.NewReader(sseOK)),
			}, nil
		}),
	}

	reqBody := `{"model":"example/calibration-model","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/v1/chat/completions", bytes.NewBufferString(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip failed: %v", err)
	}
	defer resp.Body.Close()

	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts (2 retries), got %d", attempts.Load())
	}
	if !strings.Contains(string(got), "hello intranet") {
		t.Fatalf("expected full SSE stream with business output, got: %q", string(got))
	}

	outcomes := GetRecentLLMStreamOutcomes()
	if len(outcomes) != 1 || outcomes[0].Reason != "ok" || outcomes[0].Retries507 != 2 {
		t.Fatalf("unexpected stream outcomes: %+v", outcomes)
	}
}

func TestResilienceTransport_EmptyStreamAndIdleTimeout(t *testing.T) {
	ResetPromptCalibrationForTest()
	defer ResetPromptCalibrationForTest()

	// 1. Empty 200 stream
	trEmpty := &llmResilienceTransport{
		streamIdleTimeout: 100 * time.Millisecond,
		next: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
			}, nil
		}),
	}
	req1, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1/v1/chat/completions", strings.NewReader(`{"model":"moma/dsv4"}`))
	resp1, err := trEmpty.RoundTrip(req1)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp1.Body)
	_ = resp1.Body.Close()

	// 2. Hanging stream -> idle timeout
	pr, pw := io.Pipe()
	defer pw.Close()
	trHang := &llmResilienceTransport{
		streamIdleTimeout: 40 * time.Millisecond,
		next: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       pr,
			}, nil
		}),
	}
	req2, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1/v1/chat/completions", strings.NewReader(`{"model":"moma/dsv4"}`))
	resp2, err := trHang.RoundTrip(req2)
	if err != nil {
		t.Fatal(err)
	}
	_, readErr := io.ReadAll(resp2.Body)
	_ = resp2.Body.Close()
	if !errors.Is(readErr, ErrLLMStreamIdleTimeout) {
		t.Fatalf("expected ErrLLMStreamIdleTimeout, got %v", readErr)
	}

	outcomes := GetRecentLLMStreamOutcomes()
	if len(outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %+v", outcomes)
	}
	if outcomes[0].Reason != "empty-stream" {
		t.Fatalf("expected first outcome empty-stream, got %+v", outcomes[0])
	}
	if outcomes[1].Reason != "idle-timeout" {
		t.Fatalf("expected second outcome idle-timeout, got %+v", outcomes[1])
	}
}
