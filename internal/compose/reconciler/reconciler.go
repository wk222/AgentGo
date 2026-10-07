package reconciler

import (
	"errors"
	"fmt"
	"sync"

	"agentgo/internal/compose/lifecycle"
)

// Component represents a dynamic hot-swappable module or plugin.
type Component interface {
	ID() string
	Mount(scope lifecycle.Scope) error
}

// Reconciler diffs and synchronizes the active component tree against a desired configuration state.
type Reconciler struct {
	mu        sync.Mutex
	rootScope lifecycle.Scope
	active    map[string]lifecycle.Scope
}

// New creates a new Reconciler bound to a parent root scope.
func New(rootScope lifecycle.Scope) *Reconciler {
	if rootScope == nil {
		rootScope = lifecycle.NewScope()
	}
	return &Reconciler{
		rootScope: rootScope,
		active:    make(map[string]lifecycle.Scope),
	}
}

// Reconcile computes the diff between current active components and target components,
// disposing removed components and mounting new ones without restarting the system.
func (r *Reconciler) Reconcile(desired []Component) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	desiredMap := make(map[string]Component, len(desired))
	for _, c := range desired {
		if c != nil && c.ID() != "" {
			desiredMap[c.ID()] = c
		}
	}

	var errs []error

	// 1. Identify components to remove (in active, but not in desired)
	for id, scope := range r.active {
		if _, stillWanted := desiredMap[id]; !stillWanted {
			if err := scope.Dispose(); err != nil {
				errs = append(errs, fmt.Errorf("dispose component %q failed: %w", id, err))
			}
			delete(r.active, id)
		}
	}

	// 2. Identify components to mount (in desired, but not in active)
	for id, comp := range desiredMap {
		if _, alreadyActive := r.active[id]; !alreadyActive {
			childScope := r.rootScope.Child()
			if err := comp.Mount(childScope); err != nil {
				_ = childScope.Dispose()
				errs = append(errs, fmt.Errorf("mount component %q failed: %w", id, err))
				continue
			}
			r.active[id] = childScope
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// ActiveIDs returns a list of currently mounted component IDs.
func (r *Reconciler) ActiveIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	return ids
}

// Close unloads all active components and disposes the root scope.
func (r *Reconciler) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = make(map[string]lifecycle.Scope)
	return r.rootScope.Dispose()
}
