//go:build windows

package hostui

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps helper processes (PowerShell, rundll32) from flashing a console.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
