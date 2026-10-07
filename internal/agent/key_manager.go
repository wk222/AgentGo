package agent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// APIKeyManager manages API key allocation, session affinity (KV cache hit optimization),
// and active concurrency tracking, ported and enhanced from PurrCat's key_manager.
type APIKeyManager struct {
	mu           sync.RWMutex
	usage        map[string]int    // key -> active requests count
	sessionAff   map[string]string // sessionID -> assigned key
	affinityTTL  time.Duration
	sessionTimes map[string]time.Time
}

var (
	defaultKeyManager     *APIKeyManager
	defaultKeyManagerOnce sync.Once
)

// DefaultKeyManager returns the singleton APIKeyManager.
func DefaultKeyManager() *APIKeyManager {
	defaultKeyManagerOnce.Do(func() {
		defaultKeyManager = NewAPIKeyManager(2 * time.Hour)
	})
	return defaultKeyManager
}

// NewAPIKeyManager creates a new key manager with specified session affinity TTL.
func NewAPIKeyManager(affinityTTL time.Duration) *APIKeyManager {
	if affinityTTL <= 0 {
		affinityTTL = 2 * time.Hour
	}
	km := &APIKeyManager{
		usage:        make(map[string]int),
		sessionAff:   make(map[string]string),
		affinityTTL:  affinityTTL,
		sessionTimes: make(map[string]time.Time),
	}
	return km
}

// AllocateKey allocates the best API key from validKeys.
// If sessionID is provided and has a cached key in validKeys that hasn't expired,
// that key is reused to maximize provider-side KV Cache hit rates (Anthropic/OpenAI/DeepSeek/Qwen).
// Otherwise, the least-busy key (minimum active requests) is selected.
func (m *APIKeyManager) AllocateKey(validKeys []string, sessionID string) (string, error) {
	if len(validKeys) == 0 {
		return "", errors.New("no valid API keys provided")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()

	// 1. Check session affinity for KV cache optimization
	if sessionID != "" {
		if cachedKey, ok := m.sessionAff[sessionID]; ok {
			lastUsed := m.sessionTimes[sessionID]
			if now.Sub(lastUsed) <= m.affinityTTL {
				// Verify cachedKey is still in validKeys
				for _, k := range validKeys {
					if k == cachedKey {
						m.usage[cachedKey]++
						m.sessionTimes[sessionID] = now
						return cachedKey, nil
					}
				}
			}
		}
	}

	// 2. Select least-busy key
	bestKey := validKeys[0]
	minUsage := m.usage[bestKey]
	for _, k := range validKeys[1:] {
		u := m.usage[k]
		if u < minUsage {
			minUsage = u
			bestKey = k
		}
	}

	m.usage[bestKey]++
	if sessionID != "" {
		m.sessionAff[sessionID] = bestKey
		m.sessionTimes[sessionID] = now
	}

	return bestKey, nil
}

// ReleaseKey decrements the active count for an API key.
func (m *APIKeyManager) ReleaseKey(key string) {
	if key == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.usage[key] > 0 {
		m.usage[key]--
	}
}

// GetActiveCount returns the active concurrency count for a key.
func (m *APIKeyManager) GetActiveCount(key string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.usage[key]
}

// ClearSession clears affinity for a finished session.
func (m *APIKeyManager) ClearSession(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessionAff, sessionID)
	delete(m.sessionTimes, sessionID)
}

// ConcurrencyController limits concurrent requests per API key using semaphores,
// ported from PurrCat's ConcurrencyController.
type ConcurrencyController struct {
	mu           sync.Mutex
	semaphores   map[string]chan struct{}
	defaultLimit int
}

var (
	defaultConcurrencyCtrl     *ConcurrencyController
	defaultConcurrencyCtrlOnce sync.Once
)

// DefaultConcurrencyController returns the singleton ConcurrencyController.
func DefaultConcurrencyController() *ConcurrencyController {
	defaultConcurrencyCtrlOnce.Do(func() {
		defaultConcurrencyCtrl = NewConcurrencyController(5)
	})
	return defaultConcurrencyCtrl
}

// NewConcurrencyController creates a ConcurrencyController with a default limit per key.
func NewConcurrencyController(defaultLimit int) *ConcurrencyController {
	if defaultLimit <= 0 {
		defaultLimit = 1
	}
	return &ConcurrencyController{
		semaphores:   make(map[string]chan struct{}),
		defaultLimit: defaultLimit,
	}
}

func (c *ConcurrencyController) getSemaphore(key string, limit int) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	if sem, ok := c.semaphores[key]; ok {
		return sem
	}

	effectiveLimit := limit
	if effectiveLimit <= 0 {
		effectiveLimit = c.defaultLimit
	}
	sem := make(chan struct{}, effectiveLimit)
	c.semaphores[key] = sem
	return sem
}

// Acquire waits for a slot for the given key or until ctx is cancelled.
func (c *ConcurrencyController) Acquire(ctx context.Context, key string, limit int) error {
	sem := c.getSemaphore(key, limit)
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release releases a previously acquired slot for the key.
func (c *ConcurrencyController) Release(key string) {
	c.mu.Lock()
	sem, ok := c.semaphores[key]
	c.mu.Unlock()

	if !ok {
		return
	}
	select {
	case <-sem:
	default:
	}
}

// WithExponentialBackoff executes an operation with jittered exponential backoff
// for handling transient 429 rate limit errors.
func WithExponentialBackoff(ctx context.Context, maxRetries int, baseDelay time.Duration, op func() error) error {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	if baseDelay <= 0 {
		baseDelay = 500 * time.Millisecond
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = op()
		if lastErr == nil {
			return nil
		}
		if attempt == maxRetries {
			break
		}

		// Calculate delay with jitter: base * 2^attempt + jitter
		backoff := baseDelay * (1 << attempt)
		jitter := time.Duration(rand.Int63n(int64(backoff / 2)))
		sleepDuration := backoff + jitter

		select {
		case <-time.After(sleepDuration):
		case <-ctx.Done():
			return fmt.Errorf("context cancelled during backoff: %w (last err: %v)", ctx.Err(), lastErr)
		}
	}
	return fmt.Errorf("exceeded max retries (%d): %w", maxRetries, lastErr)
}
