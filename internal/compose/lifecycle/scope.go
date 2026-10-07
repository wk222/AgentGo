package lifecycle

import (
	"errors"
	"sync"
)

var (
	ErrScopeDisposed = errors.New("lifecycle: scope is already disposed")
)

// Scope manages a set of tracked reversible effects and nested child scopes.
type Scope interface {
	// Effect executes an action and registers its returned Disposer.
	// If the scope is already disposed, Effect immediately executes the returned Disposer and returns ErrScopeDisposed.
	Effect(fn EffectFunc) (Disposer, error)

	// OnDispose registers a raw cleanup function to be executed when this scope unloads.
	OnDispose(d Disposer) Disposer

	// Dispose triggers all registered disposers in reverse order (LIFO) and terminates child scopes.
	Dispose() error

	// Child creates a nested child Scope whose lifecycle is bound to this parent Scope.
	Child() Scope

	// IsDisposed reports whether this scope has been closed.
	IsDisposed() bool
}

type scopeImpl struct {
	mu        sync.Mutex
	disposed  bool
	disposers []Disposer
	children  map[*scopeImpl]struct{}
	parent    *scopeImpl
}

// NewScope creates a root Scope.
func NewScope() Scope {
	return &scopeImpl{
		children: make(map[*scopeImpl]struct{}),
	}
}

func (s *scopeImpl) IsDisposed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disposed
}

func (s *scopeImpl) Effect(fn EffectFunc) (Disposer, error) {
	if fn == nil {
		return NoopDisposer, nil
	}

	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return nil, ErrScopeDisposed
	}
	s.mu.Unlock()

	d, err := fn()
	if err != nil {
		return nil, err
	}
	if d == nil {
		d = NoopDisposer
	}

	return s.OnDispose(d), nil
}

func (s *scopeImpl) OnDispose(d Disposer) Disposer {
	if d == nil {
		return NoopDisposer
	}

	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		_ = d()
		return NoopDisposer
	}

	s.disposers = append(s.disposers, d)
	idx := len(s.disposers) - 1
	s.mu.Unlock()

	// Return a cancelable disposer handle that allows removing from the scope early
	var once sync.Once
	return func() error {
		var err error
		once.Do(func() {
			s.mu.Lock()
			if !s.disposed && idx < len(s.disposers) && s.disposers[idx] != nil {
				s.disposers[idx] = nil
			}
			s.mu.Unlock()
			err = d()
		})
		return err
	}
}

func (s *scopeImpl) Child() Scope {
	s.mu.Lock()
	defer s.mu.Unlock()

	child := &scopeImpl{
		children: make(map[*scopeImpl]struct{}),
		parent:   s,
	}

	if s.disposed {
		child.disposed = true
		return child
	}

	s.children[child] = struct{}{}
	return child
}

func (s *scopeImpl) Dispose() error {
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return nil
	}
	s.disposed = true

	// Take snapshot of children and disposers
	childrenSnapshot := make([]*scopeImpl, 0, len(s.children))
	for c := range s.children {
		childrenSnapshot = append(childrenSnapshot, c)
	}
	s.children = nil

	disposersSnapshot := s.disposers
	s.disposers = nil

	// Remove self from parent if present
	if s.parent != nil {
		s.parent.removeChild(s)
	}
	s.mu.Unlock()

	var errs []error

	// 1. Dispose all child scopes first
	for _, c := range childrenSnapshot {
		if err := c.Dispose(); err != nil {
			errs = append(errs, err)
		}
	}

	// 2. Execute disposers in LIFO order
	for i := len(disposersSnapshot) - 1; i >= 0; i-- {
		d := disposersSnapshot[i]
		if d != nil {
			if err := d(); err != nil {
				errs = append(errs, err)
			}
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (s *scopeImpl) removeChild(child *scopeImpl) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.children, child)
}
