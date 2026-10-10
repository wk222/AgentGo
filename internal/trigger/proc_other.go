//go:build !windows

package trigger

import (
	"context"
	"syscall"
	"time"
)

// watchProcess checks liveness with signal 0 every 2s: only children can be Wait()ed on unix.
func watchProcess(ctx context.Context, pid int) (map[string]any, bool) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return map[string]any{"pid": pid, "note": "process exited (exit code unavailable)"}, true
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-tick.C:
		}
	}
}
