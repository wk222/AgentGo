package tui

import (
	"sync"

	"agentgo/internal/engine"
)

// sink collects engine events from the run goroutine. The UI drains it on a
// timer, so a token flood costs one redraw per tick instead of one per token.
type sink struct {
	mu  sync.Mutex
	buf []engine.Event
}

// Emit implements engine.EventSink.
func (s *sink) Emit(e engine.Event) {
	s.mu.Lock()
	s.buf = append(s.buf, e)
	s.mu.Unlock()
}

func (s *sink) drain() []engine.Event {
	s.mu.Lock()
	out := s.buf
	s.buf = nil
	s.mu.Unlock()
	return out
}
