package webshell

import (
	"encoding/json"
	"sync"
)

// hub fans UI events out to every connected browser tab over SSE.
type hub struct {
	mu      sync.Mutex
	clients map[*subscriber]struct{}
}

type subscriber struct {
	ch chan []byte
}

// subscriberBuffer is how many events a tab may lag behind. A tab that falls
// further behind is dropped; its EventSource reconnects on its own.
const subscriberBuffer = 8192

func newHub() *hub { return &hub{clients: map[*subscriber]struct{}{}} }

func (h *hub) subscribe() *subscriber {
	s := &subscriber{ch: make(chan []byte, subscriberBuffer)}
	h.mu.Lock()
	h.clients[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *hub) unsubscribe(s *subscriber) {
	h.mu.Lock()
	if _, ok := h.clients[s]; ok {
		delete(h.clients, s)
		close(s.ch)
	}
	h.mu.Unlock()
}

// publish encodes one event synchronously, so callers may reuse their payload
// afterwards, then queues it for every subscriber without blocking.
func (h *hub) publish(name string, data any) {
	payload, err := json.Marshal(struct {
		Name string `json:"name"`
		Data any    `json:"data"`
	}{name, data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.clients {
		select {
		case s.ch <- payload:
		default:
			delete(h.clients, s)
			close(s.ch)
		}
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
