package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// SSEWriter writes Server-Sent Events to an HTTP response.
type SSEWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func NewSSEWriter(w http.ResponseWriter) (*SSEWriter, error) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	return &SSEWriter{w: w, flusher: fl}, nil
}

func (s *SSEWriter) WriteEvent(event string, data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if event != "" {
		if _, err := fmt.Fprintf(s.w, "event: %s\n", event); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", b); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}

// Broker fans out live task events to multiple SSE subscribers.
type Broker struct {
	mu   sync.RWMutex
	subs map[string]map[int]func(event string, data []byte) //nolint:revive
	next int
}

func NewBroker() *Broker {
	return &Broker{subs: make(map[string]map[int]func(event string, data []byte))}
}

func (b *Broker) Subscribe(taskID string, fn func(event string, data []byte)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs[taskID] == nil {
		b.subs[taskID] = make(map[int]func(event string, data []byte))
	}
	id := b.next
	b.next++
	b.subs[taskID][id] = fn
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.subs[taskID], id)
	}
}

func (b *Broker) Publish(taskID, event string, data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, fn := range b.subs[taskID] {
		fn(event, data)
	}
}
