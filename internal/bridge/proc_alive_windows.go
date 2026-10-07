//go:build windows

package bridge

import (
	"errors"

	"golang.org/x/sys/windows"
)

const stillActive = 259 // STILL_ACTIVE

// processAlive reports whether pid is a running process. When it cannot tell,
// it says true: callers use this to decide whether another process's data may be
// taken over, and the safe answer to "unsure" is "leave it alone".
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// No such process: the pid is free. Any other failure (access denied)
		// means something is there.
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
