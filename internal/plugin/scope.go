// Package plugin is AgentGo's plugin host: a named-service registry, per-plugin
// disposal scopes and an event bus with middleware. It depends on the standard
// library only, so every layer can build on it.
package plugin

import (
	"errors"
	"fmt"
	"sync"
)

type disposer struct {
	label string
	fn    func()
}

// Scope collects cleanup functions and runs them in reverse order exactly once.
type Scope struct {
	name   string
	mu     sync.Mutex
	items  []disposer
	closed bool
}

func NewScope(name string) *Scope { return &Scope{name: name} }

// Effect registers a cleanup. After the scope is disposed it runs fn immediately,
// so a late registration can never leak.
func (s *Scope) Effect(label string, fn func()) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = runDisposer(disposer{label, fn})
		return
	}
	s.items = append(s.items, disposer{label, fn})
	s.mu.Unlock()
}

// Len reports pending cleanups.
func (s *Scope) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items)
}

// Dispose runs every cleanup (last registered first). A panicking cleanup is
// reported but never stops the others. Idempotent.
func (s *Scope) Dispose() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	items := s.items
	s.items = nil
	s.mu.Unlock()

	var errs []error
	for i := len(items) - 1; i >= 0; i-- {
		if err := runDisposer(items[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func runDisposer(d disposer) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("dispose %q panicked: %v", d.label, r)
		}
	}()
	d.fn()
	return nil
}
