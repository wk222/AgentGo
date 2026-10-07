//go:build !windows

package bridge

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid is a running process. When it cannot tell,
// it says true: callers use this to decide whether another process's data may be
// taken over, and the safe answer to "unsure" is "leave it alone".
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	return !errors.Is(err, syscall.ESRCH)
}
