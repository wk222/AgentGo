package scheduler

import (
	"context"
	"sync"
	"time"
)

// Runner ticks due jobs and invokes onFire (typically starts a taskhub chat job).
type Runner struct {
	store  *Store
	onFire func(ctx context.Context, job Job)
	stop   chan struct{}
	once   sync.Once
}

func NewRunner(store *Store, onFire func(ctx context.Context, job Job)) *Runner {
	return &Runner{store: store, onFire: onFire, stop: make(chan struct{})}
}

func (r *Runner) Start() {
	r.once.Do(func() {
		go r.loop()
	})
}

func (r *Runner) Stop() { close(r.stop) }

func (r *Runner) loop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			now := time.Now().Unix()
			jobs, err := r.store.Due(now)
			if err != nil {
				continue
			}
			for _, j := range jobs {
				if r.onFire != nil {
					r.onFire(context.Background(), j)
				}
				_ = r.store.BumpNext(j.ID, j.IntervalSec)
			}
		}
	}
}
