package memory

import (
	"context"
	"log"
	"sync"
	"time"

	"agentgo/internal/config"
)

// DistillScheduler processes durable learning work and discovers real episode
// scopes. A profile name is a policy, never a session storage key.
type DistillScheduler struct {
	pipeline *Pipeline
	scopeFn  func() string
	hoursFn  func() int
	mu       sync.Mutex
}

func NewDistillScheduler(p *Pipeline, scopeFn func() string, hoursFn func() int) *DistillScheduler {
	return &DistillScheduler{pipeline: p, scopeFn: scopeFn, hoursFn: hoursFn}
}

func (s *DistillScheduler) Start(ctx context.Context) {
	if s == nil || s.pipeline == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Minute)
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
	if s == nil || s.pipeline == nil || !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	if config.AutoLearn.On() {
		runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := s.pipeline.ProcessLearning(runCtx, 4)
		cancel()
		if err != nil {
			log.Print("[memory] background learning deferred; retained for retry")
		}
	}
	if ctx.Err() != nil {
		return
	}
	hours := 24
	if s.hoursFn != nil && s.hoursFn() > 0 {
		hours = s.hoursFn()
	}
	requested := ""
	if s.scopeFn != nil {
		requested = s.scopeFn()
	}
	// Apply cadence per actual scope, persist only after success, and rotate scopes
	// by last successful run. Failed jobs are not hidden until the next restart.
	rows, err := s.pipeline.store.db.QueryContext(ctx, `SELECT m.scope FROM memories m
 LEFT JOIN memory_pipeline_state ps ON ps.stage='heuristic-distill:'||m.scope
 WHERE m.modality='episode' AND m.status='active' AND (?='' OR m.scope=?)
 AND COALESCE(ps.last_run_ts,0)<=?
 GROUP BY m.scope ORDER BY COALESCE(ps.last_run_ts,0),m.scope LIMIT 4`, requested, requested, time.Now().Add(-time.Duration(hours)*time.Hour).Unix())
	if err != nil {
		log.Print("[memory] cannot discover distillation scopes")
		return
	}
	var scopes []string
	for rows.Next() {
		var scope string
		if err = rows.Scan(&scope); err != nil {
			break
		}
		scopes = append(scopes, scope)
	}
	if rowErr := rows.Err(); err == nil {
		err = rowErr
	}
	rows.Close()
	if err != nil {
		return
	}
	for _, scope := range scopes {
		if _, err = s.pipeline.Distill(ctx, scope, 30); err != nil {
			log.Print("[memory] session distillation deferred")
			continue
		}
		if err = s.pipeline.store.SetPipelineState(ctx, "heuristic-distill:"+scope, time.Now().Unix()); err != nil {
			log.Print("[memory] cannot persist distillation cadence")
		}
	}
}
