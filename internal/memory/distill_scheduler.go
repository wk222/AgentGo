package memory

import (
	"context"
	"log"
	"sync"
	"time"
)

// DistillScheduler runs lightweight memory distill on a mode-driven cadence.
type DistillScheduler struct {
	pipeline *Pipeline
	scopeFn  func() string
	hoursFn  func() int
	mu       sync.Mutex
	last     map[string]time.Time
}

func NewDistillScheduler(pipeline *Pipeline, scopeFn func() string, hoursFn func() int) *DistillScheduler {
	return &DistillScheduler{
		pipeline: pipeline,
		scopeFn:  scopeFn,
		hoursFn:  hoursFn,
		last:     make(map[string]time.Time),
	}
}

// Start begins hourly checks; distill runs when interval elapsed for current scope.
func (s *DistillScheduler) Start(ctx context.Context) {
	if s == nil || s.pipeline == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		s.tick(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.tick(ctx)
			}
		}
	}()
}

func (s *DistillScheduler) tick(ctx context.Context) {
	scope := "session"
	hours := 24
	if s.scopeFn != nil {
		if v := s.scopeFn(); v != "" {
			scope = v
		}
	}
	if s.hoursFn != nil {
		if h := s.hoursFn(); h > 0 {
			hours = h
		}
	}
	interval := time.Duration(hours) * time.Hour

	s.mu.Lock()
	prev := s.last[scope]
	if !prev.IsZero() && time.Since(prev) < interval {
		s.mu.Unlock()
		return
	}
	s.last[scope] = time.Now()
	s.mu.Unlock()

	summary, err := s.pipeline.Distill(ctx, scope, 30)
	if err != nil {
		log.Printf("[memory] mode distill %s: %v", scope, err)
		return
	}
	if summary != "" {
		log.Printf("[memory] distilled scope=%s (%d chars)", scope, len(summary))
	}
}
