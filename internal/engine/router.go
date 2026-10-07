package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrUnknownEngine = errors.New("engine: unknown engine")
	ErrNoEngine      = errors.New("engine: no engines registered")
)

// ActiveRun is a snapshot of an in-flight run.
type ActiveRun struct {
	RunID     string    `json:"run_id"`
	Engine    string    `json:"engine"`
	SessionID string    `json:"session_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

type activeRun struct {
	ActiveRun
	cancel context.CancelFunc
}

// Router registers engines, assigns run ids, tracks and cancels runs, and
// guarantees every run ends with exactly one EventDone.
type Router struct {
	mu        sync.RWMutex
	engines   map[string]AgentEngine
	gens      map[string]int64 // registration serial per engine id (guards stale dispose)
	regSeq    int64
	defaultID string
	runs      map[string]*activeRun
	seq       atomic.Int64
}

func NewRouter() *Router {
	return &Router{engines: map[string]AgentEngine{}, gens: map[string]int64{}, runs: map[string]*activeRun{}}
}

// Register adds an engine. The first registered engine becomes the default.
// The returned func removes it again (a no-op if it was since replaced); when
// the default is removed, the alphabetically first remaining engine takes over.
func (r *Router) Register(e AgentEngine) (dispose func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := e.ID()
	r.regSeq++
	seq := r.regSeq
	r.engines[id] = e
	r.gens[id] = seq
	if r.defaultID == "" {
		r.defaultID = id
	}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.gens[id] != seq {
			return
		}
		delete(r.engines, id)
		delete(r.gens, id)
		if r.defaultID != id {
			return
		}
		r.defaultID = ""
		for other := range r.engines {
			if r.defaultID == "" || other < r.defaultID {
				r.defaultID = other
			}
		}
	}
}

// SetDefault selects the engine used when RunRequest.Engine is empty.
func (r *Router) SetDefault(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.engines[id]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownEngine, id)
	}
	r.defaultID = id
	return nil
}

// Engines returns registered engine ids, sorted.
func (r *Router) Engines() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.engines))
	for id := range r.engines {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// NewRunID returns a process-unique run id.
func (r *Router) NewRunID() string {
	return fmt.Sprintf("run_%d_%d", time.Now().UnixMilli(), r.seq.Add(1))
}

func (r *Router) resolve(id string) (AgentEngine, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.engines) == 0 {
		return nil, ErrNoEngine
	}
	if id == "" {
		id = r.defaultID
	}
	e, ok := r.engines[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownEngine, id)
	}
	return e, nil
}

// Run executes req synchronously. Events go to sink (nil = discard). The result
// is also returned; the run is always finished with one EventDone.
func (r *Router) Run(ctx context.Context, req RunRequest, sink EventSink) (res RunResult) {
	if sink == nil {
		sink = DiscardSink
	}
	if req.RunID == "" {
		req.RunID = r.NewRunID()
	}
	eng, err := r.resolve(req.Engine)
	if err != nil {
		res = RunResult{RunID: req.RunID, Engine: req.Engine, Error: err.Error()}
		newEmitter(sink, req.RunID, req.SessionID, req.Engine).done(res)
		return res
	}
	req.Engine = eng.ID()

	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ar := &activeRun{
		ActiveRun: ActiveRun{RunID: req.RunID, Engine: req.Engine, SessionID: req.SessionID, StartedAt: time.Now()},
		cancel:    cancel,
	}
	r.mu.Lock()
	r.runs[req.RunID] = ar
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.runs, req.RunID)
		r.mu.Unlock()
	}()

	em := newEmitter(sink, req.RunID, req.SessionID, req.Engine)
	em.Status("started", nil)

	func() {
		defer func() {
			if p := recover(); p != nil {
				res = RunResult{Error: fmt.Sprintf("engine panic: %v", p)}
			}
		}()
		res, err = eng.Run(rctx, req, em)
	}()

	res.RunID, res.Engine = req.RunID, req.Engine
	switch {
	case errors.Is(rctx.Err(), context.Canceled) && ctx.Err() == nil:
		// cancelled via Router.Cancel (not by the caller's own ctx)
		res.Error = "cancelled"
		em.Status("cancelled", nil)
	case err != nil && res.Error == "":
		res.Error = err.Error()
	}
	if res.Error != "" && res.Error != "cancelled" {
		em.Error(res.Error)
	}
	em.done(res)
	return res
}

// Cancel cancels one run. Returns false if it is not active.
func (r *Router) Cancel(runID string) bool {
	r.mu.RLock()
	ar, ok := r.runs[runID]
	r.mu.RUnlock()
	if ok {
		ar.cancel()
	}
	return ok
}

// CancelSession cancels all active runs of a session and returns how many.
func (r *Router) CancelSession(sessionID string) int {
	r.mu.RLock()
	var targets []*activeRun
	for _, ar := range r.runs {
		if ar.SessionID == sessionID {
			targets = append(targets, ar)
		}
	}
	r.mu.RUnlock()
	for _, ar := range targets {
		ar.cancel()
	}
	return len(targets)
}

// Active returns a snapshot of in-flight runs, oldest first.
func (r *Router) Active() []ActiveRun {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ActiveRun, 0, len(r.runs))
	for _, ar := range r.runs {
		out = append(out, ar.ActiveRun)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}
