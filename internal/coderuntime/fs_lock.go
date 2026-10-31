package coderuntime

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// pathLocks serialises read-modify-write sequences on one file.
//
// A model may put several edit, multiedit or write calls for the same file into
// one reply, and the tool runner executes the calls of a reply concurrently.
// Each call reads the file, derives the new content from what it read, and
// writes that back, after waiting for approval where the policy asks for one.
// Two calls that overlap therefore start from the same bytes, and the second
// write silently discards the first. Both calls report success, so nothing in
// the transcript shows the loss.
//
// The lock is per path, so calls on different files still run side by side. A
// waiter gives up when its context ends: a call queued behind an edit that is
// waiting for a person's approval must not outlive the run it belongs to, which
// is why this is not a plain sync.Mutex.
type pathLocks struct {
	mu sync.Mutex
	m  map[string]*pathLock
}

type pathLock struct {
	token chan struct{} // capacity 1; holding the token is holding the lock
	refs  int           // the holder plus the waiters; the entry is dropped at zero
}

// acquire blocks until key is free or ctx ends. On success the returned func
// releases the lock; calling it more than once is harmless.
func (l *pathLocks) acquire(ctx context.Context, key string) (release func(), err error) {
	l.mu.Lock()
	if l.m == nil {
		l.m = make(map[string]*pathLock)
	}
	e := l.m[key]
	if e == nil {
		e = &pathLock{token: make(chan struct{}, 1)}
		l.m[key] = e
	}
	e.refs++
	l.mu.Unlock()

	select {
	case e.token <- struct{}{}:
	case <-ctx.Done():
		l.forget(key, e)
		return nil, ctx.Err()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			<-e.token
			l.forget(key, e)
		})
	}, nil
}

// forget drops one reference and removes the entry once nobody uses it, so the
// map does not grow by one lock per file ever written in the process.
func (l *pathLocks) forget(key string, e *pathLock) {
	l.mu.Lock()
	e.refs--
	if e.refs == 0 {
		delete(l.m, key)
	}
	l.mu.Unlock()
}

// size reports how many files currently have a lock entry; tests use it to
// check that idle entries are dropped.
func (l *pathLocks) size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.m)
}

// lockKey names the file a lock guards. Windows compares paths without regard
// to case because its file systems do, so C:\W\A.txt and c:\w\a.txt are one
// file there. Links are not resolved.
func lockKey(path string) string {
	p := filepath.Clean(path)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// withPathLock runs fn while holding the lock for path. Callers release it
// before follow-up work such as waiting for diagnostics, so a queue of edits to
// one file does not wait on each other's diagnostics.
func (t *fsTools) withPathLock(ctx context.Context, path string, fn func() (ToolResult, error)) (ToolResult, error) {
	release, err := t.locks.acquire(ctx, lockKey(path))
	if err != nil {
		return ToolResult{}, err
	}
	defer release()
	return fn()
}
