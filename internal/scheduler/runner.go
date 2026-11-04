package scheduler

import (
	"context"
	"sync"
	"time"
)

// Runner fires due jobs. onFire usually starts a taskhub chat job.
type Runner struct {
	store  *Store
	onFire func(ctx context.Context, job Job)
	stop   chan struct{}
	once   sync.Once
	tick   time.Duration
}

func NewRunner(store *Store, onFire func(ctx context.Context, job Job)) *Runner {
	return &Runner{store: store, onFire: onFire, stop: make(chan struct{}), tick: 30 * time.Second}
}

func (r *Runner) Start() {
	r.once.Do(func() {
		go r.loop()
	})
}

func (r *Runner) Stop() { close(r.stop) }

func (r *Runner) loop() {
	r.fireDue() // runs missed while the app was closed or the machine slept start right away
	ticker := time.NewTicker(r.tick)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			r.fireDue()
		}
	}
}

// fireDue claims and fires every job that is due. A job that was due several times while the
// machine slept fires once: the schedule resumes from now rather than replaying the backlog.
func (r *Runner) fireDue() {
	jobs, err := r.store.Due(r.store.now().Unix())
	if err != nil {
		return
	}
	for _, j := range jobs {
		claimed, ok, err := r.store.Claim(j)
		if err != nil || !ok {
			continue
		}
		if r.onFire != nil {
			r.onFire(context.Background(), claimed)
		}
	}
}
