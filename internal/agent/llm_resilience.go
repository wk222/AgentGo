package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrLLMStreamIdleTimeout is returned when an active LLM response stream produces
// zero bytes for longer than the configured stream idle timeout.
var ErrLLMStreamIdleTimeout = errors.New("llm stream idle timeout (zero bytes received within idle window)")

const (
	defaultStreamIdleTimeout     = 180 * time.Second
	defaultPreOutput507Retries   = 3
	defaultPreOutput507Backoff   = 300 * time.Millisecond
	minCalibrationPromptTokens   = 1000
	minCalibrationBytesPerToken  = 0.5
	maxCalibrationBytesPerToken  = 64.0
	maxRecentStreamOutcomesCount = 50
)

// LLMStreamOutcome records the terminal state of one LLM HTTP response stream
// (absorbed from a reference implementation).
type LLMStreamOutcome struct {
	Model      string    `json:"model"`
	Status     int       `json:"status"`
	Reason     string    `json:"reason"` // "ok" | "idle-timeout" | "empty-stream" | "upstream-error" | "pre-output-507-retry"
	Bytes      int       `json:"bytes"`
	DurationMS int64     `json:"duration_ms"`
	Retries507 int       `json:"retries_507,omitempty"`
	FinishedAt time.Time `json:"finished_at"`
}

var (
	resilienceMu            sync.RWMutex
	minBytesPerTokenByModel = make(map[string]float64)
	lastReqBytesByModel     = make(map[string]int)
	lastReqBytesAny         int
	recentStreamOutcomes    []LLMStreamOutcome
)

// RecordOutboundRequestBytes remembers the JSON payload byte size of the most recent
// outbound LLM request so the subsequent UsageObserver callback can calibrate
// bytes-per-token against the gateway's real tokenizer.
func RecordOutboundRequestBytes(modelName string, byteLen int) {
	if byteLen <= 0 {
		return
	}
	key := normalizeModelKey(modelName)
	resilienceMu.Lock()
	defer resilienceMu.Unlock()
	lastReqBytesAny = byteLen
	if key != "" {
		lastReqBytesByModel[key] = byteLen
	}
}

// RecordPromptCalibration updates the conservative minimum bytes-per-token ratio
// for modelName using real prompt_tokens reported by the upstream gateway.
func RecordPromptCalibration(modelName string, requestBytes int, promptTokens int) {
	if promptTokens < minCalibrationPromptTokens {
		return
	}
	key := normalizeModelKey(modelName)
	resilienceMu.Lock()
	defer resilienceMu.Unlock()
	if requestBytes <= 0 {
		if key != "" {
			requestBytes = lastReqBytesByModel[key]
		}
		if requestBytes <= 0 {
			requestBytes = lastReqBytesAny
		}
	}
	if requestBytes <= 0 {
		return
	}
	ratio := float64(requestBytes) / float64(promptTokens)
	if ratio < minCalibrationBytesPerToken || ratio > maxCalibrationBytesPerToken {
		return
	}
	if key == "" {
		key = "_default"
	}
	if prev, ok := minBytesPerTokenByModel[key]; !ok || ratio < prev {
		minBytesPerTokenByModel[key] = ratio
	}
}

// CalibratedBytesPerToken returns the conservative bytes-per-token ratio for modelName, if known.
func CalibratedBytesPerToken(modelName string) (float64, bool) {
	key := normalizeModelKey(modelName)
	resilienceMu.RLock()
	defer resilienceMu.RUnlock()
	if key != "" {
		if r, ok := minBytesPerTokenByModel[key]; ok {
			return r, true
		}
	}
	if r, ok := minBytesPerTokenByModel["_default"]; ok {
		return r, true
	}
	return 0, false
}

// EstimateCalibratedTokens estimates prompt tokens for text using the model's
// self-calibrated gateway tokenizer ratio when available, falling back to rune heuristic.
func EstimateCalibratedTokens(modelName, text string) int {
	if len(text) == 0 {
		return 0
	}
	if ratio, ok := CalibratedBytesPerToken(modelName); ok && ratio > 0 {
		est := int(float64(len(text))/ratio) + 1
		if htmlOrRune := estimateTokens(text); est > htmlOrRune {
			return est
		}
	}
	return estimateTokens(text)
}

// ResetPromptCalibrationForTest clears calibration state and stream outcomes.
func ResetPromptCalibrationForTest() {
	resilienceMu.Lock()
	defer resilienceMu.Unlock()
	minBytesPerTokenByModel = make(map[string]float64)
	lastReqBytesByModel = make(map[string]int)
	lastReqBytesAny = 0
	recentStreamOutcomes = nil
}

func recordLLMStreamOutcome(o LLMStreamOutcome) {
	if o.FinishedAt.IsZero() {
		o.FinishedAt = time.Now()
	}
	resilienceMu.Lock()
	defer resilienceMu.Unlock()
	recentStreamOutcomes = append(recentStreamOutcomes, o)
	if len(recentStreamOutcomes) > maxRecentStreamOutcomesCount {
		recentStreamOutcomes = append([]LLMStreamOutcome(nil),
			recentStreamOutcomes[len(recentStreamOutcomes)-maxRecentStreamOutcomesCount:]...)
	}
}

// GetRecentLLMStreamOutcomes returns a copy of the recent LLM stream outcomes ring buffer.
func GetRecentLLMStreamOutcomes() []LLMStreamOutcome {
	resilienceMu.RLock()
	defer resilienceMu.RUnlock()
	out := make([]LLMStreamOutcome, len(recentStreamOutcomes))
	copy(out, recentStreamOutcomes)
	return out
}

func normalizeModelKey(modelName string) string {
	return strings.ToLower(strings.TrimSpace(modelName))
}

// llmResilienceTransport wraps an http.RoundTripper with:
// 1. Outbound request byte recording (for self-calibrating Pre-flight Context Gate)
// 2. Pre-output SSE error.code=507 transparent retry (for MoMA / GLM-5.2 gateways)
// 3. Per-chunk Stream Idle Timeout & empty-stream detection
type llmResilienceTransport struct {
	next              http.RoundTripper
	streamIdleTimeout time.Duration
	max507Retries     int
	retry507Backoff   time.Duration
	shapeLog          bool
}

func newResilientLLMClient(connectTimeout time.Duration, shapeLog bool) *http.Client {
	if connectTimeout <= 0 {
		connectTimeout = 120 * time.Second
	}
	base := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: connectTimeout,
		TLSHandshakeTimeout:   15 * time.Second,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{
		// No fixed wall-clock Client.Timeout: ResponseHeaderTimeout bounds Stage 1 (headers),
		// and resilientStreamBody bounds Stage 2 (per-chunk idle timeout) so healthy long
		// generations are never cut off mid-stream.
		Transport: &llmResilienceTransport{
			next:              base,
			streamIdleTimeout: defaultStreamIdleTimeout,
			max507Retries:     defaultPreOutput507Retries,
			retry507Backoff:   defaultPreOutput507Backoff,
			shapeLog:          shapeLog,
		},
	}
}

func (t *llmResilienceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.next
	if base == nil {
		base = http.DefaultTransport
	}
	idleTimeout := t.streamIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultStreamIdleTimeout
	}

	var rawBody []byte
	var modelName string
	if req.Body != nil && req.Method == http.MethodPost {
		var err error
		rawBody, err = io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		modelName = extractModelFromJSON(rawBody)
		RecordOutboundRequestBytes(modelName, len(rawBody))
		if t.shapeLog {
			log.Printf("[llm-debug] %s", describeMessages(rawBody))
		}
		dumpLLMRequest(rawBody)
	}

	maxRetries := t.max507Retries
	backoff := t.retry507Backoff
	if backoff <= 0 {
		backoff = defaultPreOutput507Backoff
	}

	startedAt := time.Now()
	retries507 := 0

	for attempt := 0; ; attempt++ {
		attemptReq := req
		if rawBody != nil {
			if attempt > 0 {
				attemptReq = req.Clone(req.Context())
			}
			attemptReq.Body = io.NopCloser(bytes.NewReader(rawBody))
			attemptReq.ContentLength = int64(len(rawBody))
		}

		resp, err := base.RoundTrip(attemptReq)
		if err != nil {
			recordLLMStreamOutcome(LLMStreamOutcome{
				Model:      modelName,
				Status:     0,
				Reason:     "upstream-error",
				DurationMS: time.Since(startedAt).Milliseconds(),
				Retries507: retries507,
			})
			return nil, err
		}

		// Apply cancellation/idle protection before SSE inspection, including the first byte.
		if resp.Body != nil && req.Method == http.MethodPost {
			resp.Body = newIdleStreamBody(req.Context(), resp.Body, idleTimeout)
		}

		// Check for pre-output SSE error.code=507 on HTTP 200 event-stream responses
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		if resp.StatusCode == http.StatusOK && rawBody != nil && strings.Contains(ct, "text/event-stream") && maxRetries > 0 {
			peeked, hit507, peekErr := peekPreOutputSSE507(resp.Body)
			if hit507 && attempt < maxRetries {
				_ = resp.Body.Close()
				retries507++
				log.Printf("[llm-resilience] pre-output SSE 507 on model=%s (attempt %d/%d), retrying...", modelName, retries507, maxRetries)
				select {
				case <-req.Context().Done():
					return nil, req.Context().Err()
				case <-time.After(backoff):
					continue
				}
			}
			if peekErr != nil && len(peeked) == 0 {
				_ = resp.Body.Close()
				return nil, peekErr
			}
			resp.Body = &prefixReadCloser{
				Reader: io.MultiReader(bytes.NewReader(peeked), resp.Body),
				Closer: resp.Body,
			}
		}

		if resp.Body != nil && req.Method == http.MethodPost {
			resp.Body = newResilientStreamBody(resp.Body, modelName, resp.StatusCode, startedAt, retries507, 0)
		}
		return resp, nil
	}
}

type prefixReadCloser struct {
	io.Reader
	io.Closer
}

// peekPreOutputSSE507 inspects initial SSE events until either a business output frame
// is seen or an error.code=507 frame appears before any business output.
func peekPreOutputSSE507(body io.Reader) (consumed []byte, is507 bool, err error) {
	var buf bytes.Buffer
	tmp := make([]byte, 2048)
	for buf.Len() < 32*1024 {
		n, rerr := body.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
			data := buf.Bytes()
			if verdict := classifySSEPreOutput(data); verdict != ssePreOutputNeedMore {
				return data, verdict == ssePreOutputHit507, nil
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return buf.Bytes(), classifySSEPreOutput(buf.Bytes()) == ssePreOutputHit507, nil
			}
			return buf.Bytes(), false, rerr
		}
	}
	return buf.Bytes(), false, nil
}

type ssePreOutputVerdict int

const (
	ssePreOutputNeedMore ssePreOutputVerdict = iota
	ssePreOutputPass
	ssePreOutputHit507
)

func classifySSEPreOutput(raw []byte) ssePreOutputVerdict {
	s := string(raw)
	// Split complete SSE events separated by \n\n or \r\n\r\n
	norm := strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(norm, "\n\n")
	if len(parts) < 2 {
		return ssePreOutputNeedMore
	}
	// Inspect all complete events (all except the last trailing partial chunk)
	for i := 0; i < len(parts)-1; i++ {
		ev := strings.TrimSpace(parts[i])
		if ev == "" {
			continue
		}
		lines := strings.Split(ev, "\n")
		allComments := true
		var dataLine string
		for _, ln := range lines {
			ln = strings.TrimSpace(ln)
			if ln == "" || strings.HasPrefix(ln, ":") {
				continue
			}
			allComments = false
			if strings.HasPrefix(ln, "data:") {
				dataLine = strings.TrimSpace(strings.TrimPrefix(ln, "data:"))
				break
			}
		}
		if allComments || dataLine == "" {
			continue
		}
		if dataLine == "[DONE]" {
			return ssePreOutputPass
		}
		var payload struct {
			Type  string `json:"type"`
			Error *struct {
				Code any `json:"code"`
			} `json:"error"`
			Choices []struct {
				FinishReason *string        `json:"finish_reason"`
				Delta        map[string]any `json:"delta"`
				Message      map[string]any `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(dataLine), &payload); err != nil {
			return ssePreOutputPass
		}
		// Responses events and unknown envelopes must pass through immediately.
		// Only known, output-free chat-completion metadata is eligible for retry.
		if payload.Type != "" {
			return ssePreOutputPass
		}
		if payload.Error != nil {
			codeStr := fmt.Sprint(payload.Error.Code)
			if codeStr == "507" {
				return ssePreOutputHit507
			}
			return ssePreOutputPass
		}
		if len(payload.Choices) == 0 {
			return ssePreOutputPass
		}
		for _, ch := range payload.Choices {
			if ch.FinishReason != nil || hasDeltaBusinessOutput(ch.Delta) || hasDeltaBusinessOutput(ch.Message) {
				return ssePreOutputPass
			}
		}
	}
	return ssePreOutputNeedMore
}

func hasDeltaBusinessOutput(m map[string]any) bool {
	for k, v := range m {
		if k == "role" || v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			if val != "" {
				return true
			}
		case []any:
			if len(val) > 0 {
				return true
			}
		case map[string]any:
			if len(val) > 0 {
				return true
			}
		default:
			return true
		}
	}
	return false
}

func extractModelFromJSON(raw []byte) string {
	var body struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &body) == nil {
		return strings.TrimSpace(body.Model)
	}
	return ""
}

type resilientStreamBody struct {
	inner       io.ReadCloser
	model       string
	status      int
	startedAt   time.Time
	retries507  int
	idleTimeout time.Duration

	mu        sync.Mutex
	totalRead int
	finished  bool
}

func newResilientStreamBody(inner io.ReadCloser, model string, status int, startedAt time.Time, retries507 int, idleTimeout time.Duration) *resilientStreamBody {
	if idleTimeout > 0 {
		inner = newIdleStreamBody(context.Background(), inner, idleTimeout)
	}
	return &resilientStreamBody{
		inner:       inner,
		model:       model,
		status:      status,
		startedAt:   startedAt,
		retries507:  retries507,
		idleTimeout: idleTimeout,
	}
}

func (b *resilientStreamBody) Read(p []byte) (int, error) {
	n, err := b.inner.Read(p)
	b.addBytes(n, err)
	return n, err
}

func (b *resilientStreamBody) addBytes(n int, err error) {
	b.mu.Lock()
	b.totalRead += n
	total := b.totalRead
	b.mu.Unlock()
	if errors.Is(err, io.EOF) {
		if b.status == http.StatusOK && total == 0 {
			b.finish("empty-stream")
		} else if b.status >= 400 {
			b.finish("upstream-error")
		} else {
			b.finish("ok")
		}
	} else if errors.Is(err, ErrLLMStreamIdleTimeout) {
		b.finish("idle-timeout")
	} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		b.finish("cancelled")
	} else if err != nil {
		b.finish("upstream-error")
	}
}

func (b *resilientStreamBody) finish(reason string) {
	b.mu.Lock()
	if b.finished {
		b.mu.Unlock()
		return
	}
	b.finished = true
	total := b.totalRead
	b.mu.Unlock()

	recordLLMStreamOutcome(LLMStreamOutcome{
		Model:      b.model,
		Status:     b.status,
		Reason:     reason,
		Bytes:      total,
		DurationMS: time.Since(b.startedAt).Milliseconds(),
		Retries507: b.retries507,
	})
}

func (b *resilientStreamBody) Close() error {
	err := b.inner.Close()
	b.mu.Lock()
	total := b.totalRead
	already := b.finished
	b.mu.Unlock()
	if !already {
		reason := "ok"
		if b.status == http.StatusOK && total == 0 {
			reason = "empty-stream"
		} else if b.status >= 400 {
			reason = "upstream-error"
		}
		b.finish(reason)
	}
	return err
}
