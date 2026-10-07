package bridge

import (
	"context"
	"sync"
	"time"
)

// approvalRelay tells a surface that is waiting on an approval that another
// surface took the decision.
//
// A run started in the Crush TUI blocks inside the TUI's permission dialog. If
// the desktop UI approves the same request first, the queue records that one
// decision and the desktop call resumes the run — but the TUI would keep
// showing the dialog and never see the outcome. The relay closes that gap in
// two steps, both published from resolveApproval (the only place a decision is
// acted on):
//
//	decided    the decision is on record   -> the waiting surface closes its dialog
//	completed  the resumed run has finished -> the waiting surface shows the result
//
// The relay is process-local on purpose: it connects surfaces that share a host
// process. Surfaces in different processes share only the database, which is
// why the TUI attaches to the host instead of running its own.
//
// The zero value is ready to use.
type approvalRelay struct {
	mu      sync.Mutex
	entries map[string]*relayEntry
}

type relayEntry struct {
	decided chan struct{} // closed when the decision is recorded
	done    chan struct{} // closed when the resumed run is finished
	// Set before the matching channel is closed; read only after it.
	approved bool
	by       string
	out      map[string]any
	finished time.Time
}

const (
	relayKeep    = 10 * time.Minute // how long a finished decision stays readable
	relayMaxSize = 256
)

// entry returns the record for an approval, creating it. Callers hold no lock.
func (r *approvalRelay) entry(id string) *relayEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = map[string]*relayEntry{}
	}
	e := r.entries[id]
	if e == nil {
		r.pruneLocked()
		e = &relayEntry{decided: make(chan struct{}), done: make(chan struct{})}
		r.entries[id] = e
	}
	return e
}

// pruneLocked forgets decisions nobody can still be waiting for.
func (r *approvalRelay) pruneLocked() {
	if len(r.entries) < relayMaxSize {
		return
	}
	for id, e := range r.entries {
		if !e.finished.IsZero() && time.Since(e.finished) > relayKeep {
			delete(r.entries, id)
		}
	}
}

// markDecided records that approvalID was decided by `by`. The first call
// wins; the queue guarantees there is only one.
func (r *approvalRelay) markDecided(id string, approved bool, by string) {
	e := r.entry(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-e.decided:
		return
	default:
	}
	e.approved, e.by = approved, by
	close(e.decided)
}

// markCompleted publishes what the decision led to. It is also safe to call
// without markDecided (the decision then counts as taken).
func (r *approvalRelay) markCompleted(id string, out map[string]any) {
	e := r.entry(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-e.done:
		return
	default:
	}
	e.out = out
	e.finished = time.Now()
	close(e.done)
	select {
	case <-e.decided:
	default:
		close(e.decided)
	}
}

// relayWatch is one waiter's view of an approval.
type relayWatch struct {
	e *relayEntry
}

// watch starts following an approval. A decision made before the call is seen
// immediately, so there is no window between "approval created" and "waiter
// attached" in which a decision can be missed.
func (r *approvalRelay) watch(id string) relayWatch { return relayWatch{e: r.entry(id)} }

// Decision delivers the approved/rejected value once someone else decides, for
// use as Emitter.AwaitPermission's `other`. The goroutine ends with ctx or when
// stop is called.
func (w relayWatch) Decision(ctx context.Context) (ch <-chan bool, stop func()) {
	out := make(chan bool, 1)
	quit := make(chan struct{})
	var once sync.Once
	go func() {
		select {
		case <-w.e.decided:
			out <- w.e.approved
		case <-quit:
		case <-ctx.Done():
		}
	}()
	return out, func() { once.Do(func() { close(quit) }) }
}

// DecidedBy names the surface that decided ("" while undecided).
func (w relayWatch) DecidedBy() string {
	select {
	case <-w.e.decided:
		return w.e.by
	default:
		return ""
	}
}

// Decided reports whether a decision is on record, waiting up to grace for one
// that is being recorded at this very moment.
func (w relayWatch) Decided(grace time.Duration) bool {
	select {
	case <-w.e.decided:
		return true
	default:
	}
	if grace <= 0 {
		return false
	}
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-w.e.decided:
		return true
	case <-t.C:
		return false
	}
}

// Outcome waits for the resumed run to finish and returns the result of
// resolveApproval. ok is false if ctx ended first.
func (w relayWatch) Outcome(ctx context.Context) (approved bool, out map[string]any, ok bool) {
	select {
	case <-w.e.done:
		return w.e.approved, w.e.out, true
	case <-ctx.Done():
		return false, nil, false
	}
}
