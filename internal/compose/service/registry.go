package service

import (
	"errors"
	"sync"

	"agentgo/internal/compose/lifecycle"
)

var (
	ErrServiceAlreadyRegistered = errors.New("service: service name already registered")
)

type defaultRegistry struct {
	mu          sync.RWMutex
	services    map[string]Service
	statuses    map[string]Status
	subscribers map[int]ChangeHandler
	nextSubID   int
}

// NewRegistry creates a new service registry.
func NewRegistry() Registry {
	return &defaultRegistry{
		services:    make(map[string]Service),
		statuses:    make(map[string]Status),
		subscribers: make(map[int]ChangeHandler),
	}
}

func (r *defaultRegistry) Register(svc Service) (lifecycle.Disposer, error) {
	if svc == nil || svc.Name() == "" {
		return lifecycle.NoopDisposer, errors.New("service: invalid service instance")
	}

	name := svc.Name()
	r.mu.Lock()
	if _, exists := r.services[name]; exists {
		r.mu.Unlock()
		return lifecycle.NoopDisposer, ErrServiceAlreadyRegistered
	}

	r.services[name] = svc
	initialStatus := svc.Status()
	r.statuses[name] = initialStatus
	r.mu.Unlock()

	r.notifyChange(name, StatusUnavailable, initialStatus)

	// Return a reversible disposer that completely unregisters the service on cleanup
	return func() error {
		r.mu.Lock()
		oldStatus, hasOld := r.statuses[name]
		delete(r.services, name)
		delete(r.statuses, name)
		r.mu.Unlock()

		if hasOld {
			r.notifyChange(name, oldStatus, StatusUnavailable)
		}
		return nil
	}, nil
}

func (r *defaultRegistry) Get(name string) (Service, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	svc, ok := r.services[name]
	return svc, ok
}

func (r *defaultRegistry) Status(name string) Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if st, ok := r.statuses[name]; ok {
		return st
	}
	return StatusUnavailable
}

func (r *defaultRegistry) SetStatus(name string, newStatus Status) {
	r.mu.Lock()
	oldStatus, exists := r.statuses[name]
	if !exists {
		oldStatus = StatusUnavailable
	}
	if oldStatus == newStatus {
		r.mu.Unlock()
		return
	}
	r.statuses[name] = newStatus
	r.mu.Unlock()

	r.notifyChange(name, oldStatus, newStatus)
}

func (r *defaultRegistry) Subscribe(handler ChangeHandler) lifecycle.Disposer {
	if handler == nil {
		return lifecycle.NoopDisposer
	}

	r.mu.Lock()
	id := r.nextSubID
	r.nextSubID++
	r.subscribers[id] = handler
	r.mu.Unlock()

	return func() error {
		r.mu.Lock()
		delete(r.subscribers, id)
		r.mu.Unlock()
		return nil
	}
}

func (r *defaultRegistry) CheckCoeffects(requiredServices ...string) (bool, []string) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var missing []string
	for _, req := range requiredServices {
		st, exists := r.statuses[req]
		if !exists || st != StatusReady {
			missing = append(missing, req)
		}
	}

	return len(missing) == 0, missing
}

func (r *defaultRegistry) AllServices() map[string]Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Status, len(r.statuses))
	for k, v := range r.statuses {
		out[k] = v
	}
	return out
}

func (r *defaultRegistry) notifyChange(name string, oldStatus, newStatus Status) {
	r.mu.RLock()
	handlers := make([]ChangeHandler, 0, len(r.subscribers))
	for _, h := range r.subscribers {
		handlers = append(handlers, h)
	}
	r.mu.RUnlock()

	for _, h := range handlers {
		h(name, oldStatus, newStatus)
	}
}
