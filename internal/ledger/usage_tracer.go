package ledger

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DailyModelUsage holds aggregated usage metrics for a specific model and masked key.
type DailyModelUsage struct {
	Model            string  `json:"model"`
	APIKey           string  `json:"api_key"`
	Calls            int     `json:"calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	ReasoningTokens  int     `json:"reasoning_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	TotalDurationSec float64 `json:"total_duration_sec"`
}

// ModelUsageTracer performs in-memory delta aggregation with periodic atomic flush to disk,
// ported and enhanced from PurrCat's ModelUsageTracer.
type ModelUsageTracer struct {
	mu            sync.Mutex
	baseDir       string
	pendingDeltas map[string]map[string]*DailyModelUsage // date -> (hashKey -> stats)
	flushInterval time.Duration
	stopCh        chan struct{}
	closeOnce     sync.Once
	wg            sync.WaitGroup
}

// NewModelUsageTracer creates a ModelUsageTracer saving daily rollups under baseDir.
func NewModelUsageTracer(baseDir string, flushInterval time.Duration) *ModelUsageTracer {
	if flushInterval <= 0 {
		flushInterval = 60 * time.Second
	}
	_ = os.MkdirAll(baseDir, 0755)

	tracer := &ModelUsageTracer{
		baseDir:       baseDir,
		pendingDeltas: make(map[string]map[string]*DailyModelUsage),
		flushInterval: flushInterval,
		stopCh:        make(chan struct{}),
	}

	tracer.wg.Add(1)
	go tracer.flushLoop()
	return tracer
}

// MaskAPIKey masks sensitive API keys for privacy in logs and daily metrics.
func MaskAPIKey(apiKey string) string {
	if apiKey == "" {
		return "UNKNOWN"
	}
	if len(apiKey) <= 10 {
		return "***"
	}
	return fmt.Sprintf("%s***%s", apiKey[:6], apiKey[len(apiKey)-4:])
}

// Record captures an LLM call usage delta.
func (t *ModelUsageTracer) Record(model, apiKey string, promptTokens, cachedTokens, completionTokens, reasoningTokens int, duration time.Duration) {
	dateStr := time.Now().Format("2006-01-02")
	maskedKey := MaskAPIKey(apiKey)
	hashKey := fmt.Sprintf("%s|%s", model, maskedKey)

	totalTokens := promptTokens + completionTokens
	durationSec := duration.Seconds()

	t.mu.Lock()
	defer t.mu.Unlock()

	dateMap, ok := t.pendingDeltas[dateStr]
	if !ok {
		dateMap = make(map[string]*DailyModelUsage)
		t.pendingDeltas[dateStr] = dateMap
	}

	entry, ok := dateMap[hashKey]
	if !ok {
		entry = &DailyModelUsage{
			Model:  model,
			APIKey: maskedKey,
		}
		dateMap[hashKey] = entry
	}

	entry.Calls++
	entry.PromptTokens += promptTokens
	entry.CachedTokens += cachedTokens
	entry.CompletionTokens += completionTokens
	entry.ReasoningTokens += reasoningTokens
	entry.TotalTokens += totalTokens
	entry.TotalDurationSec += durationSec
}

// Flush writes all pending in-memory deltas into disk storage with atomic file rename.
func (t *ModelUsageTracer) Flush() error {
	t.mu.Lock()
	if len(t.pendingDeltas) == 0 {
		t.mu.Unlock()
		return nil
	}
	deltasToFlush := t.pendingDeltas
	t.pendingDeltas = make(map[string]map[string]*DailyModelUsage)
	t.mu.Unlock()

	for dateStr, dateMap := range deltasToFlush {
		filePath := filepath.Join(t.baseDir, fmt.Sprintf("%s.json", dateStr))

		existingData := make(map[string]*DailyModelUsage)
		if data, err := os.ReadFile(filePath); err == nil {
			_ = json.Unmarshal(data, &existingData)
		}

		for hashKey, delta := range dateMap {
			if curr, exists := existingData[hashKey]; exists {
				curr.Calls += delta.Calls
				curr.PromptTokens += delta.PromptTokens
				curr.CachedTokens += delta.CachedTokens
				curr.CompletionTokens += delta.CompletionTokens
				curr.ReasoningTokens += delta.ReasoningTokens
				curr.TotalTokens += delta.TotalTokens
				curr.TotalDurationSec += delta.TotalDurationSec
			} else {
				existingData[hashKey] = delta
			}
		}

		// Atomic write: write to temp file then rename
		tmpPath := filePath + ".tmp"
		payload, err := json.MarshalIndent(existingData, "", "  ")
		if err != nil {
			continue
		}
		if err := os.WriteFile(tmpPath, payload, 0644); err != nil {
			continue
		}
		_ = os.Rename(tmpPath, filePath)
	}

	return nil
}

// GetDailySummary returns today's total usage breakdown across all models.
func (t *ModelUsageTracer) GetDailySummary(dateStr string) (map[string]*DailyModelUsage, error) {
	// First flush memory deltas
	_ = t.Flush()

	if dateStr == "" {
		dateStr = time.Now().Format("2006-01-02")
	}
	filePath := filepath.Join(t.baseDir, fmt.Sprintf("%s.json", dateStr))
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*DailyModelUsage), nil
		}
		return nil, err
	}

	res := make(map[string]*DailyModelUsage)
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return res, nil
}

func (t *ModelUsageTracer) flushLoop() {
	defer t.wg.Done()
	ticker := time.NewTicker(t.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			_ = t.Flush()
		case <-t.stopCh:
			_ = t.Flush()
			return
		}
	}
}

// Close stops the background flush goroutine and performs a final disk flush.
// It is safe to call more than once.
func (t *ModelUsageTracer) Close() error {
	t.closeOnce.Do(func() { close(t.stopCh) })
	t.wg.Wait()
	return t.Flush()
}
