package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// FireFunc performs the trigger's action (wake a session, emit a workflow signal).
type FireFunc func(ctx context.Context, t Trigger, ev Event) error

var ErrNotArmed = errors.New("trigger is not armed")

type Engine struct {
	store *Store
	fire  FireFunc

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	ctx     context.Context
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

func NewEngine(store *Store, fire FireFunc) *Engine {
	return &Engine{store: store, fire: fire, cancels: map[string]context.CancelFunc{}}
}

// Start re-arms every persisted armed trigger and re-delivers fired-but-undelivered ones
// (a crash between "fired" and "delivered").
func (e *Engine) Start(parent context.Context) error {
	e.mu.Lock()
	e.ctx, e.stop = context.WithCancel(parent)
	e.mu.Unlock()

	pending, err := e.store.ListUndelivered()
	if err != nil {
		return err
	}
	for _, t := range pending {
		t := t
		e.wg.Add(1)
		go func() {
			defer e.wg.Done()
			e.deliver(t, Event{TriggerID: t.ID, Kind: t.Kind, At: time.Now().Unix(), Detail: map[string]any{"redelivered": true}})
		}()
	}
	armed, err := e.store.ListArmed()
	if err != nil {
		return err
	}
	for _, t := range armed {
		e.arm(t)
	}
	return nil
}

func (e *Engine) Stop() {
	e.mu.Lock()
	stop := e.stop
	e.mu.Unlock()
	if stop != nil {
		stop()
	}
	e.wg.Wait()
}

// Add stores and arms a new trigger.
func (e *Engine) Add(t Trigger) (Trigger, error) {
	created, err := e.store.Create(t)
	if err != nil {
		return Trigger{}, err
	}
	e.arm(created)
	return created, nil
}

func (e *Engine) List() ([]Trigger, error)      { return e.store.List() }
func (e *Engine) Get(id string) (Trigger, error) { return e.store.Get(id) }

// Cancel disarms a trigger. Cancelling one that already fired reports ErrNotArmed.
func (e *Engine) Cancel(id string) error {
	ok, err := e.store.Cancel(id)
	if err != nil {
		return err
	}
	e.disarm(id)
	if !ok {
		return ErrNotArmed
	}
	return nil
}

// FireWebhook fires a webhook trigger (or any armed trigger, for manual testing) with a payload.
func (e *Engine) FireWebhook(id string, payload map[string]any) error {
	t, err := e.store.Get(id)
	if err != nil {
		return err
	}
	if t.Status != StatusArmed {
		return ErrNotArmed
	}
	if !e.hit(t, payload) {
		return ErrNotArmed
	}
	return nil
}

func (e *Engine) arm(t Trigger) {
	e.mu.Lock()
	base := e.ctx
	if base == nil {
		base = context.Background()
	}
	if _, exists := e.cancels[t.ID]; exists {
		e.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(base)
	e.cancels[t.ID] = cancel
	e.mu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer e.disarm(t.ID)

		if t.ExpiresAt > 0 {
			var tcancel context.CancelFunc
			remain := time.Until(time.Unix(t.ExpiresAt, 0))
			ctx, tcancel = context.WithTimeout(ctx, max(remain, 0))
			defer tcancel()
		}
		detail, ok := watchTrigger(ctx, t)
		if ok {
			e.hit(t, detail)
			return
		}
		if t.ExpiresAt > 0 && ctx.Err() == context.DeadlineExceeded {
			if done, _ := e.store.Expire(t.ID); done {
				log.Printf("[trigger] %s expired", t.ID)
			}
		}
	}()
}

func (e *Engine) disarm(id string) {
	e.mu.Lock()
	if c, ok := e.cancels[id]; ok {
		c()
		delete(e.cancels, id)
	}
	e.mu.Unlock()
}

// hit claims the trigger (exactly once) and then delivers it.
func (e *Engine) hit(t Trigger, detail map[string]any) bool {
	ev := Event{TriggerID: t.ID, Kind: t.Kind, At: time.Now().Unix(), Detail: detail}
	raw, _ := json.Marshal(ev)
	won, err := e.store.MarkFired(t.ID, string(raw))
	if err != nil {
		log.Printf("[trigger] %s mark fired: %v", t.ID, err)
		return false
	}
	if !won {
		return false
	}
	e.deliver(t, ev)
	return true
}

func (e *Engine) deliver(t Trigger, ev Event) {
	ctx := e.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	raw, _ := json.Marshal(ev)
	if err := e.fire(ctx, t, ev); err != nil {
		log.Printf("[trigger] %s delivery failed: %v", t.ID, err)
		// stays undelivered: it will be retried on the next start
		_ = e.store.setResult(t.ID, fmt.Sprintf("delivery error: %v | event: %s", err, raw))
		return
	}
	_ = e.store.MarkDelivered(t.ID, string(raw))
}
