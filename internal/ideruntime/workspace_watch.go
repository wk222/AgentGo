package ideruntime

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Directory-watch registration used to be a synchronous, unbounded recursive walk that ran inside
// the "ide" plugin's Apply. On a broad workspace (e.g. %LOCALAPPDATA% on a corporate laptop, tens of
// thousands of directories) that blocked application startup for minutes and exhausted handles.
//
// Now: breadth-first (shallow dirs first), a hard cap on watched directories, and only a short
// synchronous budget — the remainder continues in the background without blocking startup.

const (
	defaultMaxWatchDirs    = 4000
	defaultWatchSyncBudget = 500 * time.Millisecond
)

// watchSkipDirs are never watched or descended into: generated, vendored or cache trees that are
// large and whose changes the IDE does not surface.
var watchSkipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	".idea":        true,
	"target":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".cache":       true,
	".gradle":      true,
	".next":        true,
}

// SetWatchLimits overrides the watched-directory cap and the synchronous registration budget.
// maxDirs <= 0 disables the cap. Takes effect on the next watcher (re)start.
func (fs *WorkspaceFS) SetWatchLimits(maxDirs int, syncBudget time.Duration) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.maxWatchDirs = maxDirs
	fs.syncBudget = syncBudget
}

// WatchedDirCount reports how many directories currently carry a watch.
func (fs *WorkspaceFS) WatchedDirCount() int {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return len(fs.watchDirs)
}

// WaitWatchIdle blocks until background watch registration has finished (event-based; for tests
// and for callers that must not read WatchedDirCount mid-walk).
func (fs *WorkspaceFS) WaitWatchIdle() { fs.walkWG.Wait() }

// addWatchDir registers one directory. stop=true means this walk must end (cap reached or the
// watcher was replaced).
//
// fs.mu is never held across w.Add: on Windows Add blocks until fsnotify's reader goroutine
// replies, and that goroutine can be waiting on the event loop, which itself needs fs.mu.
// Holding the lock across Add therefore deadlocks (observed as a 600s test timeout). The
// directory is reserved under the lock first so concurrent walks cannot double-add or
// overshoot the cap, and the reservation is rolled back if Add fails.
func (fs *WorkspaceFS) addWatchDir(w *fsnotify.Watcher, dir string) (stop bool) {
	fs.mu.Lock()
	if fs.watcher != w {
		fs.mu.Unlock()
		return true // superseded by SetRoot/StopWatcher
	}
	if fs.watchDirs[dir] {
		fs.mu.Unlock()
		return false
	}
	if fs.maxWatchDirs > 0 && len(fs.watchDirs) >= fs.maxWatchDirs {
		if !fs.capLogged {
			fs.capLogged = true
			log.Printf("[workspace] watching capped at %d directories under %s; deeper changes need a manual refresh", fs.maxWatchDirs, fs.rootPath)
		}
		fs.mu.Unlock()
		return true
	}
	fs.watchDirs[dir] = true // reserve
	fs.mu.Unlock()

	if err := w.Add(dir); err != nil {
		fs.mu.Lock()
		if fs.watcher == w {
			delete(fs.watchDirs, dir)
		}
		fs.mu.Unlock()
	}
	return false
}

// walkWatch registers watches breadth-first starting at roots. With a non-zero deadline it returns
// the unvisited frontier once the deadline passes; the caller hands that to continueWalk.
// Must be called WITHOUT fs.mu held.
func (fs *WorkspaceFS) walkWatch(w *fsnotify.Watcher, roots []string, deadline time.Time) []string {
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		if !deadline.IsZero() && time.Now().After(deadline) {
			return queue
		}
		dir := queue[0]
		queue = queue[1:]
		if fs.addWatchDir(w, dir) {
			return nil
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && !watchSkipDirs[e.Name()] {
				queue = append(queue, filepath.Join(dir, e.Name()))
			}
		}
	}
	return nil
}

// continueWalk finishes a partially completed registration in the background.
func (fs *WorkspaceFS) continueWalk(w *fsnotify.Watcher, frontier []string) {
	if len(frontier) == 0 {
		return
	}
	fs.walkWG.Add(1)
	go func() {
		defer fs.walkWG.Done()
		fs.walkWatch(w, frontier, time.Time{})
	}()
}
