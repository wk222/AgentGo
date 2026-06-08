package workflow

import (
	"context"
	"sync"
	"time"
)

// SignalBus coordinates wait_signal / emit_signal between workflow runs.
type SignalBus struct {
	mu      sync.Mutex
	waiters map[string]chan string // key: runID|signal
	values  map[string]string
}

var defaultSignalBus = NewSignalBus()

// DefaultSignalBus returns the process-wide signal bus.
func DefaultSignalBus() *SignalBus { return defaultSignalBus }

func NewSignalBus() *SignalBus {
	return &SignalBus{
		waiters: make(map[string]chan string),
		values:  make(map[string]string),
	}
}

func signalKey(runID, name string) string {
	return runID + "|" + name
}

// Emit delivers a signal payload to waiters (or stores for late wait).
func (b *SignalBus) Emit(runID, name, payload string) {
	if b == nil || name == "" {
		return
	}
	key := signalKey(runID, name)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.values[key] = payload
	if ch, ok := b.waiters[key]; ok {
		select {
		case ch <- payload:
		default:
		}
		delete(b.waiters, key)
	}
}

// Wait blocks until signal or timeout.
func (b *SignalBus) Wait(ctx context.Context, runID, name string, timeout time.Duration) (string, error) {
	if b == nil {
		return "", context.DeadlineExceeded
	}
	key := signalKey(runID, name)
	b.mu.Lock()
	if v, ok := b.values[key]; ok {
		delete(b.values, key)
		b.mu.Unlock()
		return v, nil
	}
	ch := make(chan string, 1)
	b.waiters[key] = ch
	b.mu.Unlock()

	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		b.cancelWait(key, ch)
		return "", ctx.Err()
	case v := <-ch:
		return v, nil
	case <-timer.C:
		b.cancelWait(key, ch)
		return "", context.DeadlineExceeded
	}
}

func (b *SignalBus) cancelWait(key string, ch chan string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cur, ok := b.waiters[key]; ok && cur == ch {
		delete(b.waiters, key)
	}
}
