package plugin

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNextTwice is returned when a handler calls next more than once.
var ErrNextTwice = errors.New("plugin: next() called more than once")

// Handler is event middleware. Call next to continue the chain (and finally the
// emitter's terminal action); return without calling it to short-circuit.
type Handler func(payload any, next func() error) error

type handlerEntry struct {
	id   int
	prio int
	h    Handler
}

type eventBus struct {
	mu       sync.RWMutex
	seq      int
	handlers map[string][]*handlerEntry
}

func newEventBus() *eventBus { return &eventBus{handlers: map[string][]*handlerEntry{}} }

func (b *eventBus) on(event string, prio int, h Handler) (dispose func()) {
	b.mu.Lock()
	b.seq++
	e := &handlerEntry{id: b.seq, prio: prio, h: h}
	b.handlers[event] = append(b.handlers[event], e)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.handlers[event]
		for i, x := range list {
			if x.id == e.id {
				b.handlers[event] = append(append([]*handlerEntry(nil), list[:i]...), list[i+1:]...)
				return
			}
		}
	}
}

// emit runs handlers ordered by (priority, registration); lower priority runs
// first. final is the terminal action reached when every handler calls next.
func (b *eventBus) emit(event string, payload any, final func() error) error {
	b.mu.RLock()
	list := append([]*handlerEntry(nil), b.handlers[event]...)
	b.mu.RUnlock()
	sort.SliceStable(list, func(i, j int) bool { return list[i].prio < list[j].prio })

	var run func(i int) error
	run = func(i int) error {
		if i >= len(list) {
			if final != nil {
				return final()
			}
			return nil
		}
		called := false
		next := func() error {
			if called {
				return ErrNextTwice
			}
			called = true
			return run(i + 1)
		}
		return safeHandle(list[i].h, payload, next)
	}
	return run(0)
}

func safeHandle(h Handler, payload any, next func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("plugin: event handler panicked: %v", r)
		}
	}()
	return h(payload, next)
}
