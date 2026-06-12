package workflow

import (
	"context"
	"sync"
	"time"
)

// LeaseStore coordinates exclusive workflow resources (PyFlow lease node subset).
type LeaseStore struct {
	mu     sync.Mutex
	holder map[string]leaseEntry
}

type leaseEntry struct {
	owner     string
	expiresAt time.Time
}

func NewLeaseStore() *LeaseStore {
	return &LeaseStore{holder: make(map[string]leaseEntry)}
}

var defaultLeases = NewLeaseStore()

func DefaultLeaseStore() *LeaseStore { return defaultLeases }

func (s *LeaseStore) Acquire(key, owner string, ttl time.Duration) bool {
	if s == nil || key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if e, ok := s.holder[key]; ok && now.Before(e.expiresAt) && e.owner != owner {
		return false
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	s.holder[key] = leaseEntry{owner: owner, expiresAt: now.Add(ttl)}
	return true
}

func (s *LeaseStore) Release(key, owner string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.holder[key]; ok && e.owner == owner {
		delete(s.holder, key)
	}
}

// WaitAcquire spins until lease acquired or context cancelled.
func (s *LeaseStore) WaitAcquire(ctx context.Context, key, owner string, ttl, poll time.Duration) error {
	if poll <= 0 {
		poll = 200 * time.Millisecond
	}
	for {
		if s.Acquire(key, owner, ttl) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}
